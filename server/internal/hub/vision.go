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

// visionCmd carries out find_image, tap_image, ocr, ocr_find and ocr_tap on the PC.
func (h *Hub) visionCmd(d *Device, cmd *spec.Command, p map[string]any) Result {
	start := time.Now()
	timeout, _ := p["timeout"].(float64)
	deadline := start.Add(time.Duration(timeout * float64(time.Second)))
	bad := func(reason string) Result { return h.errResult("BAD_ARGS", map[string]any{"cmd": cmd.Name, "reason": reason}) }

	switch cmd.Name {
	case "find_image", "tap_image":
		raw, err := base64.StdEncoding.DecodeString(fmt.Sprint(p["image"]))
		if err != nil {
			return bad("image is not base64")
		}
		tmpl, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return bad("image is not a PNG or JPEG: " + err.Error())
		}
		threshold, _ := p["threshold"].(float64)
		for {
			_, shot, errR := h.screenshot(d)
			if errR != nil {
				return errR
			}
			m, ok, err := vision.Find(shot, tmpl, threshold)
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
