package webdriver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type routeFn func(r *http.Request, s *session, body map[string]any) (any, error)

type route struct {
	pattern string
	fn      routeFn
}

func (b *Bridge) routes() []route {
	el := func(fn func(r *http.Request, s *session, node map[string]any, body map[string]any) (any, error)) routeFn {
		return func(r *http.Request, s *session, body map[string]any) (any, error) {
			node, err := s.element(r.PathValue("eid"))
			if err != nil {
				return nil, err
			}
			return fn(r, s, node, body)
		}
	}
	return []route{
		{"GET /status", func(*http.Request, *session, map[string]any) (any, error) {
			return map[string]any{"ready": true, "message": "Droidline WebDriver bridge", "build": map[string]any{"version": b.version}}, nil
		}},
		{"POST /session", b.newSession},
		{"GET /sessions", func(*http.Request, *session, map[string]any) (any, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			list := []any{}
			for _, s := range b.sessions {
				list = append(list, map[string]any{"id": s.id, "capabilities": s.caps})
			}
			return list, nil
		}},
		{"GET /session/{sid}", func(_ *http.Request, s *session, _ map[string]any) (any, error) { return s.caps, nil }},
		{"DELETE /session/{sid}", b.deleteSession},
		{"GET /session/{sid}/timeouts", func(_ *http.Request, s *session, _ map[string]any) (any, error) {
			return map[string]any{"implicit": int(s.implicit * 1000), "pageLoad": 300000, "script": 30000}, nil
		}},
		{"POST /session/{sid}/timeouts", func(_ *http.Request, s *session, body map[string]any) (any, error) {
			if v, ok := body["implicit"].(float64); ok {
				s.implicit = v / 1000
			}
			if v, ok := body["ms"].(float64); ok && body["type"] == "implicit" {
				s.implicit = v / 1000
			}
			return nil, nil
		}},
		{"POST /session/{sid}/timeouts/implicit_wait", func(_ *http.Request, s *session, body map[string]any) (any, error) {
			if v, ok := body["ms"].(float64); ok {
				s.implicit = v / 1000
			}
			return nil, nil
		}},
		{"GET /session/{sid}/source", func(_ *http.Request, s *session, _ map[string]any) (any, error) {
			doc, err := b.document(s)
			if err != nil {
				return nil, err
			}
			return pageSource(doc), nil
		}},
		{"GET /session/{sid}/screenshot", func(_ *http.Request, s *session, _ map[string]any) (any, error) {
			res, err := b.call(s, map[string]any{"cmd": "screenshot", "format": "png"})
			if err != nil {
				return nil, err
			}
			return res["data"], nil
		}},
		{"POST /session/{sid}/element", func(_ *http.Request, s *session, body map[string]any) (any, error) {
			return b.findOne(s, body, nil)
		}},
		{"POST /session/{sid}/elements", func(_ *http.Request, s *session, body map[string]any) (any, error) {
			return b.findMany(s, body, nil)
		}},
		{"POST /session/{sid}/element/{eid}/element", el(func(_ *http.Request, s *session, node, body map[string]any) (any, error) {
			return b.findOne(s, body, node)
		})},
		{"POST /session/{sid}/element/{eid}/elements", el(func(_ *http.Request, s *session, node, body map[string]any) (any, error) {
			return b.findMany(s, body, node)
		})},
		{"POST /session/{sid}/element/{eid}/click", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			_, err := b.call(s, map[string]any{"cmd": "touch", "by": query(node), "timeout": 0})
			return nil, stale(err)
		})},
		{"POST /session/{sid}/element/{eid}/value", el(func(_ *http.Request, s *session, node, body map[string]any) (any, error) {
			text, _ := body["text"].(string)
			if text == "" {
				if list, ok := body["value"].([]any); ok {
					for _, c := range list {
						text += fmt.Sprint(c)
					}
				}
			}
			_, err := b.call(s, map[string]any{"cmd": "input", "by": query(node), "text": text, "timeout": 0})
			return nil, stale(err)
		})},
		{"POST /session/{sid}/element/{eid}/clear", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			_, err := b.call(s, map[string]any{"cmd": "clear", "by": query(node), "timeout": 0})
			return nil, stale(err)
		})},
		{"GET /session/{sid}/element/{eid}/text", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			fresh, err := b.refresh(s, node)
			if err != nil {
				return nil, err
			}
			return fresh["text"], nil
		})},
		{"GET /session/{sid}/element/{eid}/name", el(func(_ *http.Request, _ *session, node, _ map[string]any) (any, error) {
			return node["class"], nil
		})},
		{"GET /session/{sid}/element/{eid}/attribute/{name}", el(func(r *http.Request, s *session, node, _ map[string]any) (any, error) {
			return b.attribute(s, node, r.PathValue("name"))
		})},
		{"GET /session/{sid}/element/{eid}/property/{name}", el(func(r *http.Request, s *session, node, _ map[string]any) (any, error) {
			return b.attribute(s, node, r.PathValue("name"))
		})},
		{"GET /session/{sid}/element/{eid}/rect", el(func(_ *http.Request, _ *session, node, _ map[string]any) (any, error) {
			return rect(node), nil
		})},
		{"GET /session/{sid}/element/{eid}/location", el(func(_ *http.Request, _ *session, node, _ map[string]any) (any, error) {
			r := rect(node)
			return map[string]any{"x": r["x"], "y": r["y"]}, nil
		})},
		{"GET /session/{sid}/element/{eid}/size", el(func(_ *http.Request, _ *session, node, _ map[string]any) (any, error) {
			r := rect(node)
			return map[string]any{"width": r["width"], "height": r["height"]}, nil
		})},
		{"GET /session/{sid}/element/{eid}/displayed", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			fresh, err := b.refresh(s, node)
			return err == nil && shown(fresh), nil
		})},
		{"GET /session/{sid}/element/{eid}/enabled", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			fresh, err := b.refresh(s, node)
			if err != nil {
				return nil, err
			}
			return fresh["enabled"] == true, nil
		})},
		{"GET /session/{sid}/element/{eid}/selected", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			fresh, err := b.refresh(s, node)
			if err != nil {
				return nil, err
			}
			return fresh["checked"] == true || fresh["selected"] == true, nil
		})},
		{"GET /session/{sid}/element/{eid}/screenshot", el(func(_ *http.Request, s *session, node, _ map[string]any) (any, error) {
			return b.elementShot(s, node)
		})},
		{"POST /session/{sid}/actions", b.actions},
		{"DELETE /session/{sid}/actions", func(*http.Request, *session, map[string]any) (any, error) { return nil, nil }},
		{"POST /session/{sid}/back", b.simple("back")},
		{"GET /session/{sid}/window/rect", b.windowRect},
		{"GET /session/{sid}/window/current/size", b.windowRect},
		{"GET /session/{sid}/window/size", b.windowRect},
		{"GET /session/{sid}/orientation", func(_ *http.Request, s *session, _ map[string]any) (any, error) {
			res, err := b.call(s, map[string]any{"cmd": "orientation"})
			if err != nil {
				return nil, err
			}
			if v := fmt.Sprint(res["value"]); strings.Contains(v, "land") {
				return "LANDSCAPE", nil
			}
			return "PORTRAIT", nil
		}},
		{"GET /session/{sid}/contexts", func(*http.Request, *session, map[string]any) (any, error) { return []any{"NATIVE_APP"}, nil }},
		{"GET /session/{sid}/context", func(*http.Request, *session, map[string]any) (any, error) { return "NATIVE_APP", nil }},
		{"POST /session/{sid}/context", func(_ *http.Request, _ *session, body map[string]any) (any, error) {
			if body["name"] != "NATIVE_APP" {
				return nil, errf(500, "unsupported operation", "Droidline drives native apps only; there is no WEBVIEW context")
			}
			return nil, nil
		}},
		{"GET /session/{sid}/appium/settings", func(*http.Request, *session, map[string]any) (any, error) { return map[string]any{}, nil }},
		{"POST /session/{sid}/appium/settings", func(*http.Request, *session, map[string]any) (any, error) { return nil, nil }},
		{"POST /session/{sid}/execute/sync", b.execute},
		{"POST /session/{sid}/execute", b.execute},
		{"POST /session/{sid}/appium/device/activate_app", b.appCmd("launch")},
		{"POST /session/{sid}/appium/device/terminate_app", func(*http.Request, *session, map[string]any) (any, error) {
			return nil, errf(500, "unsupported operation", "Droidline has no force stop command, because the Settings buttons differ by phone maker and language; see the force stop recipe at droidline.dev/docs/recipes/")
		}},
		{"POST /session/{sid}/appium/device/app_state", b.appState},
		{"POST /session/{sid}/appium/device/press_keycode", func(_ *http.Request, s *session, body map[string]any) (any, error) {
			_, err := b.call(s, map[string]any{"cmd": "sendkey", "key": body["keycode"]})
			return nil, err
		}},
		{"POST /session/{sid}/appium/device/hide_keyboard", func(_ *http.Request, s *session, _ map[string]any) (any, error) {
			res, err := b.call(s, map[string]any{"cmd": "keyboard_shown"})
			if err == nil && res["value"] == true {
				_, err = b.call(s, map[string]any{"cmd": "back"})
			}
			return nil, err
		}},
		{"GET /session/{sid}/appium/device/is_keyboard_shown", b.valueOf("keyboard_shown")},
		{"GET /session/{sid}/appium/device/current_activity", b.currentField("activity")},
		{"GET /session/{sid}/appium/device/current_package", b.currentField("package")},
		{"POST /session/{sid}/appium/device/open_notifications", b.simple("open_notifications")},
		{"POST /session/{sid}/appium/device/lock", b.simple("lock")},
		{"POST /session/{sid}/appium/device/unlock", b.simple("wake")},
		{"GET /session/{sid}/appium/device/is_locked", b.valueOf("locked")},
	}
}

// stale reports an element that can no longer be found as W3C's stale element reference.
func stale(err error) error {
	if we, ok := err.(*wdError); ok && we.code == "no such element" {
		return errf(404, "stale element reference", "the element is no longer on screen: %s", we.msg)
	}
	return err
}

func (b *Bridge) refresh(s *session, node map[string]any) (map[string]any, error) {
	res, err := b.call(s, map[string]any{"cmd": "find", "by": query(node), "timeout": 0})
	if err != nil {
		return nil, stale(err)
	}
	fresh, _ := res["value"].(map[string]any)
	return fresh, nil
}

func (b *Bridge) simple(cmd string) routeFn {
	return func(_ *http.Request, s *session, _ map[string]any) (any, error) {
		_, err := b.call(s, map[string]any{"cmd": cmd})
		return nil, err
	}
}

func (b *Bridge) valueOf(cmd string) routeFn {
	return func(_ *http.Request, s *session, _ map[string]any) (any, error) {
		res, err := b.call(s, map[string]any{"cmd": cmd})
		if err != nil {
			return nil, err
		}
		return res["value"], nil
	}
}

func (b *Bridge) currentField(field string) routeFn {
	return func(_ *http.Request, s *session, _ map[string]any) (any, error) {
		res, err := b.call(s, map[string]any{"cmd": "current"})
		if err != nil {
			return nil, err
		}
		return res[field], nil
	}
}

func appID(body map[string]any) string {
	for _, k := range []string{"appId", "bundleId", "app"} {
		if v, ok := body[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (b *Bridge) appCmd(cmd string) routeFn {
	return func(_ *http.Request, s *session, body map[string]any) (any, error) {
		id := appID(body)
		if id == "" {
			return nil, errInvalidArg("appId is required")
		}
		_, err := b.call(s, map[string]any{"cmd": cmd, "package": id})
		return nil, err
	}
}

// appState answers with Appium's codes: 0 not installed, 1 not running, 4 in front.
func (b *Bridge) appState(_ *http.Request, s *session, body map[string]any) (any, error) {
	id := appID(body)
	res, err := b.call(s, map[string]any{"cmd": "installed", "package": id})
	if err != nil {
		return nil, err
	}
	if res["value"] == "" {
		return 0, nil
	}
	res, err = b.call(s, map[string]any{"cmd": "in_app", "package": id})
	if err != nil {
		return nil, err
	}
	if res["value"] == true {
		return 4, nil
	}
	return 1, nil
}

func (b *Bridge) windowRect(_ *http.Request, s *session, _ map[string]any) (any, error) {
	res, err := b.call(s, map[string]any{"cmd": "info"})
	if err != nil {
		return nil, err
	}
	return map[string]any{"x": 0, "y": 0, "width": res["width"], "height": res["height"]}, nil
}

// shown is the node's visible field; phones that predate it send none, and those nodes count as shown.
func shown(node map[string]any) bool {
	v, ok := node["visible"].(bool)
	return !ok || v
}

func rect(node map[string]any) map[string]any {
	b, _ := node["bounds"].([]any)
	n := func(i int) float64 {
		if i < len(b) {
			f, _ := b[i].(float64)
			return f
		}
		return 0
	}
	return map[string]any{"x": n(0), "y": n(1), "width": n(2) - n(0), "height": n(3) - n(1)}
}

var attrNames = map[string]string{
	"text": "text", "resource-id": "id", "resourceId": "id", "content-desc": "desc", "contentDescription": "desc",
	"class": "class", "className": "class", "package": "package", "checkable": "checkable", "checked": "checked",
	"clickable": "clickable", "enabled": "enabled", "focused": "focused", "long-clickable": "long_clickable",
	"longClickable": "long_clickable", "password": "password", "scrollable": "scrollable", "selected": "selected",
}

// attribute answers like UiAutomator2: strings, with flags as "true" or "false".
func (b *Bridge) attribute(s *session, node map[string]any, name string) (any, error) {
	fresh, err := b.refresh(s, node)
	if err != nil {
		return nil, err
	}
	switch name {
	case "bounds":
		r := rect(fresh)
		return fmt.Sprintf("[%v,%v][%v,%v]", r["x"], r["y"], r["x"].(float64)+r["width"].(float64), r["y"].(float64)+r["height"].(float64)), nil
	case "displayed":
		return strconv.FormatBool(shown(fresh)), nil
	case "name":
		if d, _ := fresh["desc"].(string); d != "" {
			return d, nil
		}
		return fresh["text"], nil
	}
	key, ok := attrNames[name]
	if !ok {
		return nil, nil
	}
	switch v := fresh[key].(type) {
	case bool:
		return fmt.Sprint(v), nil
	case nil:
		return nil, nil
	default:
		return fmt.Sprint(v), nil
	}
}

func (b *Bridge) elementShot(s *session, node map[string]any) (any, error) {
	res, err := b.call(s, map[string]any{"cmd": "screenshot", "format": "png"})
	if err != nil {
		return nil, err
	}
	raw, _ := base64.StdEncoding.DecodeString(fmt.Sprint(res["data"]))
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errf(500, "unknown error", "screenshot could not be decoded")
	}
	r := rect(node)
	area := image.Rect(int(r["x"].(float64)), int(r["y"].(float64)), int(r["x"].(float64)+r["width"].(float64)), int(r["y"].(float64)+r["height"].(float64)))
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return nil, errf(500, "unknown error", "screenshot cannot be cropped")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, sub.SubImage(area.Intersect(img.Bounds()))); err != nil {
		return nil, err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// --- Finding elements ---

func (b *Bridge) findOne(s *session, body map[string]any, within map[string]any) (any, error) {
	found, err := b.locate(s, body, within, false)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, errNoSuchElement(fmt.Sprintf("nothing matched %v %q", body["using"], body["value"]))
	}
	return s.remember(found[0]), nil
}

func (b *Bridge) findMany(s *session, body map[string]any, within map[string]any) (any, error) {
	found, err := b.locate(s, body, within, true)
	if we, ok := err.(*wdError); ok && we.code == "no such element" {
		return []any{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, n := range found {
		out = append(out, s.remember(n))
	}
	return out, nil
}

func (b *Bridge) locate(s *session, body map[string]any, within map[string]any, many bool) ([]map[string]any, error) {
	using, _ := body["using"].(string)
	value, _ := body["value"].(string)
	var q map[string]any
	nth := 0
	scroll := false
	switch using {
	case "xpath":
		return b.xpathFind(s, value, within, many)
	case "id":
		q = map[string]any{"id": value}
	case "accessibility id":
		q = map[string]any{"desc": value}
	case "class name":
		q = map[string]any{"class": value}
	case "name":
		q = map[string]any{"text": value}
	case "css selector":
		var err error
		if q, err = cssQuery(value); err != nil {
			return nil, err
		}
	case "-android uiautomator":
		sel, err := parseUiSelector(value)
		if err != nil {
			return nil, errInvalidArg(err.Error())
		}
		q, nth, scroll = sel.query, sel.instance, sel.scroll
	case "-droidline query":
		if err := json.Unmarshal([]byte(value), &q); err != nil {
			return nil, errInvalidArg("-droidline query must be a JSON query object: " + err.Error())
		}
	default:
		return nil, errInvalidArg(fmt.Sprintf("locator strategy %q is not supported; use xpath, id, accessibility id, class name, -android uiautomator or -droidline query", using))
	}
	if within != nil {
		if inner, ok := q["inside"].(map[string]any); ok {
			if _, nested := inner["inside"]; !nested {
				inner["inside"] = query(within)
			}
		} else {
			q["inside"] = query(within)
		}
	}
	if scroll {
		if _, err := b.call(s, map[string]any{"cmd": "scroll_to", "by": q, "nth": nth}); err != nil {
			return nil, err
		}
	}
	if !many {
		res, err := b.call(s, map[string]any{"cmd": "find", "by": q, "nth": nth, "timeout": s.implicit})
		if err != nil {
			return nil, err
		}
		n, _ := res["value"].(map[string]any)
		return []map[string]any{n}, nil
	}
	res, err := b.call(s, map[string]any{"cmd": "find_all", "by": q, "timeout": s.implicit})
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	list, _ := res["value"].([]any)
	for i, v := range list {
		if n, ok := v.(map[string]any); ok && (nth == 0 || i == nth) {
			out = append(out, n)
		}
	}
	return out, nil
}

var (
	cssID   = regexp.MustCompile(`^#([\w.:/-]+)$`)
	cssAttr = regexp.MustCompile(`^\*?\[(id|name|text|class)\s*=\s*["']([^"']*)["']\]$`)
	cssCls  = regexp.MustCompile(`^\.([\w.$-]+)$`)
)

// cssQuery covers what Selenium clients turn By.ID, By.NAME and By.CLASS_NAME into.
func cssQuery(v string) (map[string]any, error) {
	v = strings.TrimSpace(v)
	if m := cssID.FindStringSubmatch(v); m != nil {
		return map[string]any{"id": strings.ReplaceAll(m[1], `\`, "")}, nil
	}
	if m := cssAttr.FindStringSubmatch(v); m != nil {
		key := map[string]string{"id": "id", "name": "text", "text": "text", "class": "class"}[m[1]]
		return map[string]any{key: m[2]}, nil
	}
	if m := cssCls.FindStringSubmatch(v); m != nil {
		return map[string]any{"class": m[1]}, nil
	}
	return nil, errInvalidArg("only #id, .class and [id=...], [name=...] CSS selectors are supported; use xpath or -android uiautomator for more")
}

func (b *Bridge) document(s *session) (*xnode, error) {
	res, err := b.call(s, map[string]any{"cmd": "dump"})
	if err != nil {
		return nil, err
	}
	return document(res), nil
}

func (b *Bridge) xpathFind(s *session, expr string, within map[string]any, many bool) ([]map[string]any, error) {
	deadline := time.Now().Add(time.Duration(s.implicit * float64(time.Second)))
	for {
		doc, err := b.document(s)
		if err != nil {
			return nil, err
		}
		at := doc
		if within != nil {
			if at = doc.find(within); at == nil {
				return nil, errf(404, "stale element reference", "the element is no longer on screen")
			}
		}
		found, err := selectXPath(doc, at, expr)
		if err != nil {
			return nil, errInvalidArg("xpath: " + err.Error())
		}
		if len(found) > 0 || time.Now().After(deadline) {
			if !many && len(found) > 1 {
				found = found[:1]
			}
			return found, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// --- Gestures, keys and mobile: scripts ---

type point struct{ x, y float64 }

// actions turns W3C pointer and key actions into taps, long taps, swipes and key presses.
func (b *Bridge) actions(_ *http.Request, s *session, body map[string]any) (any, error) {
	sources, _ := body["actions"].([]any)
	pointers := 0
	for _, src := range sources {
		m, _ := src.(map[string]any)
		switch m["type"] {
		case "pointer":
			pointers++
			if pointers > 1 {
				return nil, errf(500, "unsupported operation", "multi-touch gestures are not supported")
			}
			if err := b.pointer(s, m); err != nil {
				return nil, err
			}
		case "key":
			if err := b.keys(s, m); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func num(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }

func (b *Bridge) pointer(s *session, src map[string]any) error {
	steps, _ := src["actions"].([]any)
	var pos, down point
	var held bool
	var ms float64
	for _, st := range steps {
		a, _ := st.(map[string]any)
		switch a["type"] {
		case "pointerMove":
			next := point{num(a, "x"), num(a, "y")}
			switch origin := a["origin"].(type) {
			case string:
				if origin == "pointer" {
					next = point{pos.x + next.x, pos.y + next.y}
				}
			case map[string]any:
				node, err := s.element(elementID(origin))
				if err != nil {
					return err
				}
				r := rect(node)
				next = point{r["x"].(float64) + r["width"].(float64)/2 + next.x, r["y"].(float64) + r["height"].(float64)/2 + next.y}
			}
			if held {
				ms += num(a, "duration")
			}
			pos = next
		case "pause":
			if held {
				ms += num(a, "duration")
			}
		case "pointerDown":
			held, down, ms = true, pos, 0
		case "pointerUp":
			if !held {
				continue
			}
			held = false
			var err error
			if math.Hypot(pos.x-down.x, pos.y-down.y) < 10 {
				if ms >= 500 {
					_, err = b.call(s, map[string]any{"cmd": "long_tap", "x": int(down.x), "y": int(down.y), "ms": int(ms)})
				} else {
					_, err = b.call(s, map[string]any{"cmd": "tap", "x": int(down.x), "y": int(down.y)})
				}
			} else {
				_, err = b.call(s, map[string]any{"cmd": "swipe", "x1": int(down.x), "y1": int(down.y), "x2": int(pos.x), "y2": int(pos.y), "ms": int(max(ms, 100))})
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Selenium's key codes for the keys a phone has.
var specialKeys = map[rune]string{'': "enter", '': "enter", '': "del", '': "tab", '': "back", '': "forward_del"}

func (b *Bridge) keys(s *session, src map[string]any) error {
	steps, _ := src["actions"].([]any)
	var text strings.Builder
	flush := func() error {
		if text.Len() == 0 {
			return nil
		}
		_, err := b.call(s, map[string]any{"cmd": "sendkey", "text": text.String()})
		text.Reset()
		return err
	}
	for _, st := range steps {
		a, _ := st.(map[string]any)
		if a["type"] != "keyDown" {
			continue
		}
		for _, r := range fmt.Sprint(a["value"]) {
			if key, ok := specialKeys[r]; ok {
				if err := flush(); err != nil {
					return err
				}
				if _, err := b.call(s, map[string]any{"cmd": "sendkey", "key": key}); err != nil {
					return err
				}
				continue
			}
			text.WriteRune(r)
		}
	}
	return flush()
}

// execute handles "mobile: ..." scripts. "mobile: droidline" runs any Droidline command:
// driver.execute_script("mobile: droidline", {"cmd": "wait_idle"}).
func (b *Bridge) execute(_ *http.Request, s *session, body map[string]any) (any, error) {
	script, _ := body["script"].(string)
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(script), "mobile:"))
	if name == strings.TrimSpace(script) {
		return nil, errf(500, "unsupported operation", "only mobile: scripts are supported; native apps have no JavaScript")
	}
	args := map[string]any{}
	if list, ok := body["args"].([]any); ok && len(list) > 0 {
		if m, ok := list[0].(map[string]any); ok {
			args = m
		}
	}
	at := func() (int, int, error) {
		if id, ok := args["elementId"].(string); ok && id != "" {
			node, err := s.element(id)
			if err != nil {
				return 0, 0, err
			}
			r := rect(node)
			return int(r["x"].(float64) + r["width"].(float64)/2), int(r["y"].(float64) + r["height"].(float64)/2), nil
		}
		return int(num(args, "x")), int(num(args, "y")), nil
	}
	switch name {
	case "droidline":
		req := map[string]any{}
		for k, v := range args {
			req[k] = v
		}
		res, err := b.call(s, req)
		if err != nil {
			return nil, err
		}
		delete(res, "ok")
		if v, only := res["value"]; only && len(res) == 1 {
			return v, nil
		}
		return res, nil
	case "clearApp":
		_, err := b.call(s, map[string]any{"cmd": "clear_data", "package": appID(args)})
		return nil, err
	case "activateApp":
		_, err := b.call(s, map[string]any{"cmd": "launch", "package": appID(args)})
		return nil, err
	case "terminateApp":
		return nil, errf(500, "unsupported operation", "Droidline has no force stop command, because the Settings buttons differ by phone maker and language; see the force stop recipe at droidline.dev/docs/recipes/")
	case "pressKey":
		_, err := b.call(s, map[string]any{"cmd": "sendkey", "key": args["keycode"]})
		return nil, err
	case "clickGesture":
		x, y, err := at()
		if err != nil {
			return nil, err
		}
		_, err = b.call(s, map[string]any{"cmd": "tap", "x": x, "y": y})
		return nil, err
	case "longClickGesture":
		x, y, err := at()
		if err != nil {
			return nil, err
		}
		req := map[string]any{"cmd": "long_tap", "x": x, "y": y}
		if d := num(args, "duration"); d > 0 {
			req["ms"] = int(d)
		}
		_, err = b.call(s, req)
		return nil, err
	case "shell":
		return nil, errf(500, "unsupported operation", "mobile: shell needs ADB, which Droidline does not use")
	}
	return nil, errf(404, "unknown method", "mobile: %s is not supported; mobile: droidline runs any Droidline command", name)
}
