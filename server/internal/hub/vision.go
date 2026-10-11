package hub

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/vision"
	"github.com/KnifeLemon/Droidline/spec"
)

// deviceCall runs one phone command on behalf of a server-side command.
func (h *Hub) deviceCall(d *Device, name string, params map[string]any) Result {
	cmd, norm, err := h.spec.Normalize(name, params)
	if err != nil {
		return h.errResult("INTERNAL", map[string]any{"reason": err.Error()})
	}
	offline := time.Duration(h.store.Config.OfflineWait * float64(time.Second))
	return <-d.Submit(&Call{Cmd: cmd, Params: norm, OfflineWait: offline})
}

// SetOCRPath points OCR at a tesseract binary; tests use it to simulate a missing install.
func (h *Hub) SetOCRPath(path string) { h.store.Config.OCR.Tesseract = path }

func failed(r Result) bool { ok, _ := r["ok"].(bool); return !ok }

// screenshot fetches a full-size PNG from the phone.
func (h *Hub) screenshot(d *Device) ([]byte, image.Image, Result) {
	r := h.deviceCall(d, "screenshot", map[string]any{"format": "png"})
	if failed(r) {
		return nil, nil, r
	}
	data, err := base64.StdEncoding.DecodeString(fmt.Sprint(r["data"]))
	if err != nil {
		return nil, nil, h.errResult("INTERNAL", map[string]any{"reason": "screenshot was not base64"})
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, h.errResult("INTERNAL", map[string]any{"reason": "screenshot could not be decoded: " + err.Error()})
	}
	return data, img, nil
}

func (h *Hub) notFoundOn(d *Device, cmd, target string, timeout float64) Result {
	screen := ""
	if r := h.deviceCall(d, "current", map[string]any{}); !failed(r) {
		screen = fmt.Sprintf("%v / %v", r["package"], r["activity"])
	}
	return h.errResult("NOT_FOUND", map[string]any{"cmd": cmd, "target": target, "timeout": timeout, "screen": screen})
}

// visionCmd carries out the picture commands (find_image, tap_image, image_exists, find_images, wait_image_gone)
// and the OCR commands (ocr, ocr_find, ocr_tap) on the PC.
func (h *Hub) visionCmd(d *Device, cmd *spec.Command, p map[string]any) Result {
	start := time.Now()
	timeout, _ := p["timeout"].(float64)
	deadline := start.Add(time.Duration(timeout * float64(time.Second)))
	bad := func(reason string) Result { return h.errResult("BAD_ARGS", map[string]any{"cmd": cmd.Name, "reason": reason}) }

	switch cmd.Name {
	case "find_image", "tap_image", "image_exists", "find_images", "wait_image_gone":
		raw, err := base64.StdEncoding.DecodeString(fmt.Sprint(p["image"]))
		if err != nil {
			return bad("image is not base64")
		}
		tmpl, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return bad("image is not a PNG or JPEG: " + err.Error())
		}
		threshold, _ := p["threshold"].(float64)
		opts, reason := imageOptions(p)
		if reason != "" {
			return bad(reason)
		}
		switch cmd.Name {
		case "image_exists":
			_, shot, errR := h.screenshot(d)
			if errR != nil {
				return errR
			}
			_, ok, err := vision.FindWith(shot, tmpl, threshold, opts)
			if err != nil {
				return bad(err.Error())
			}
			return Result{"ok": true, "value": ok}
		case "find_images":
			limit := 20
			if n, ok := p["max"].(float64); ok && n > 0 {
				limit = int(n)
			}
			for {
				_, shot, errR := h.screenshot(d)
				if errR != nil {
					return errR
				}
				ms, err := vision.FindAll(shot, tmpl, threshold, opts, limit)
				if err != nil {
					return bad(err.Error())
				}
				if len(ms) > 0 || !time.Now().Before(deadline) {
					list := make([]any, len(ms))
					for i, m := range ms {
						x, y := m.Center()
						list[i] = map[string]any{"x": x, "y": y, "bounds": m.Bounds[:], "score": round3(m.Score)}
					}
					return Result{"ok": true, "value": list}
				}
				time.Sleep(300 * time.Millisecond)
			}
		case "wait_image_gone":
			for {
				_, shot, errR := h.screenshot(d)
				if errR != nil {
					return errR
				}
				_, ok, err := vision.FindWith(shot, tmpl, threshold, opts)
				if err != nil {
					return bad(err.Error())
				}
				if !ok {
					return Result{"ok": true, "ms": time.Since(start).Milliseconds()}
				}
				if time.Now().After(deadline) {
					return h.errResult("TIMEOUT", map[string]any{"cmd": cmd.Name, "timeout": timeout})
				}
				time.Sleep(300 * time.Millisecond)
			}
		}
		for {
			_, shot, errR := h.screenshot(d)
			if errR != nil {
				return errR
			}
			m, ok, err := vision.FindWith(shot, tmpl, threshold, opts)
			if err != nil {
				return bad(err.Error())
			}
			if ok {
				x, y := m.Center()
				res := Result{"ok": true, "x": x, "y": y, "score": round3(m.Score)}
				if cmd.Name == "find_image" {
					res["bounds"] = m.Bounds[:]
					return res
				}
				if r := h.deviceCall(d, "tap", map[string]any{"x": x, "y": y}); failed(r) {
					return r
				}
				res["ms"] = time.Since(start).Milliseconds()
				return res
			}
			if time.Now().After(deadline) {
				return h.notFoundOn(d, cmd.Name, fmt.Sprintf("image (best score %.2f)", m.Score), timeout)
			}
			time.Sleep(300 * time.Millisecond)
		}

	case "ocr", "ocr_find", "ocr_tap":
		bin, err := vision.Tesseract(h.store.Config.OCR.Tesseract)
		if err != nil {
			return h.errResult("OCR_UNAVAILABLE", map[string]any{"reason": ocrReason(err)})
		}
		lang, _ := p["lang"].(string)
		want, _ := p["text"].(string)
		for {
			png, _, errR := h.screenshot(d)
			if errR != nil {
				return errR
			}
			lines, err := vision.OCR(bin, png, lang, 60*time.Second)
			if errors.Is(err, vision.ErrNoTesseract) {
				return h.errResult("OCR_UNAVAILABLE", map[string]any{"reason": ocrReason(err)})
			}
			if err != nil {
				return h.errResult("INTERNAL", map[string]any{"reason": err.Error()})
			}
			if cmd.Name == "ocr" {
				var texts []string
				list := []any{}
				for _, l := range lines {
					texts = append(texts, l.Text)
					list = append(list, map[string]any{"text": l.Text, "bounds": l.Bounds[:], "conf": round3(l.Conf)})
				}
				return Result{"ok": true, "text": strings.Join(texts, "\n"), "lines": list}
			}
			if l, ok := vision.FindText(lines, want); ok {
				x, y := (l.Bounds[0]+l.Bounds[2])/2, (l.Bounds[1]+l.Bounds[3])/2
				res := Result{"ok": true, "text": l.Text, "x": x, "y": y}
				if cmd.Name == "ocr_find" {
					res["bounds"] = l.Bounds[:]
					return res
				}
				if r := h.deviceCall(d, "tap", map[string]any{"x": x, "y": y}); failed(r) {
					return r
				}
				res["ms"] = time.Since(start).Milliseconds()
				return res
			}
			if time.Now().After(deadline) {
				return h.notFoundOn(d, cmd.Name, fmt.Sprintf("text '%s'", want), timeout)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	return h.errResult("UNKNOWN_CMD", map[string]any{"cmd": cmd.Name, "agent": "server " + Version})
}

func ocrReason(err error) string {
	msg := err.Error()
	if msg == vision.ErrNoTesseract.Error() {
		return "it was not found. Install it, then put it on PATH or set [ocr] tesseract in config.toml"
	}
	return strings.TrimPrefix(msg, vision.ErrNoTesseract.Error()+": ")
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

// imageOptions reads region, scales and color for the picture commands. A non-empty reason means a bad argument.
func imageOptions(p map[string]any) (vision.Options, string) {
	var o vision.Options
	if list, ok := p["region"].([]any); ok && len(list) > 0 {
		if len(list) != 4 {
			return o, "region needs four numbers: left, top, right, bottom"
		}
		for _, v := range list {
			f, _ := v.(float64)
			o.Region = append(o.Region, int(f))
		}
		if o.Region[2] <= o.Region[0] || o.Region[3] <= o.Region[1] {
			return o, "region must have right greater than left and bottom greater than top"
		}
	}
	if list, ok := p["scales"].([]any); ok {
		for _, v := range list {
			f, _ := v.(float64)
			if f <= 0 || f > 4 {
				return o, "each scale must be above 0 and at most 4"
			}
			o.Scales = append(o.Scales, f)
		}
	}
	o.Color, _ = p["color"].(bool)
	return o, ""
}
