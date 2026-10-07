package fakeagent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"sync"
	"time"
)

const (
	screenW = 1080
	screenH = 2400
	demoPkg = "dev.droidline.demo"
	homePkg = "com.android.launcher3"
)

type node struct {
	Text, ID, Desc, Class string
	Bounds                [4]int
	Clickable, Checkable  bool
	Checked, Selected     bool
	Editable, Password    bool
	Disabled              bool
	onClick               func(u *phoneUI)
}

// phoneUI models a launcher and a demo app with a login and a main screen.
type phoneUI struct {
	mu        sync.Mutex
	pkg, act  string
	email     string
	autoLogin bool
	adShown   bool
	screenOn  bool
	airplane  bool
	clip      string
	toast     string
	toastAt   time.Time
	focused   string
	notifs    []map[string]any
	proxyURL  string
	proxyApps []any
}

func newPhoneUI() *phoneUI {
	return &phoneUI{pkg: homePkg, act: ".Launcher", screenOn: true, adShown: true}
}

func id(name string) string { return demoPkg + ":id/" + name }

func (u *phoneUI) nodes() []*node {
	switch {
	case u.pkg == homePkg:
		return []*node{
			{Text: "Demo", Desc: "Demo", Class: "android.widget.TextView", Bounds: [4]int{80, 1800, 280, 2000}, Clickable: true,
				onClick: func(u *phoneUI) { u.open(demoPkg, ".LoginActivity") }},
			{Text: "Chrome", Desc: "Chrome", Class: "android.widget.TextView", Bounds: [4]int{320, 1800, 520, 2000}, Clickable: true,
				onClick: func(u *phoneUI) { u.open("com.android.chrome", "org.chromium.chrome.browser.ChromeTabbedActivity") }},
		}
	case u.pkg == demoPkg && u.act == ".LoginActivity":
		ns := []*node{
			{Text: "Sign in", Class: "android.widget.TextView", Bounds: [4]int{60, 200, 1020, 320}},
			{Text: u.email, ID: id("email"), Desc: "Email", Class: "android.widget.EditText", Bounds: [4]int{60, 500, 1020, 640}, Clickable: true, Editable: true},
			{ID: id("password"), Desc: "Password", Class: "android.widget.EditText", Bounds: [4]int{60, 680, 1020, 820}, Clickable: true, Editable: true, Password: true},
			{Text: "Keep me signed in", ID: id("auto_login"), Class: "android.widget.CheckBox", Bounds: [4]int{60, 860, 1020, 960},
				Clickable: true, Checkable: true, Checked: u.autoLogin, onClick: func(u *phoneUI) { u.autoLogin = !u.autoLogin }},
			{Text: "Log in", ID: id("login"), Class: "android.widget.Button", Bounds: [4]int{60, 1000, 1020, 1140}, Clickable: true,
				Disabled: u.email == "", onClick: func(u *phoneUI) { u.open(demoPkg, ".MainActivity") }},
		}
		if u.adShown {
			ns = append(ns, &node{Text: "Close ad", ID: id("ad_close"), Class: "android.widget.Button", Bounds: [4]int{760, 2200, 1020, 2320},
				Clickable: true, onClick: func(u *phoneUI) { u.adShown = false }})
		}
		return ns
	case u.pkg == demoPkg && u.act == ".MainActivity":
		ns := []*node{
			{Text: "Welcome, " + u.email, ID: id("greeting"), Class: "android.widget.TextView", Bounds: [4]int{60, 200, 1020, 320}},
			{Text: "Chats", ID: id("main_tab"), Class: "android.widget.TextView", Bounds: [4]int{0, 2240, 540, 2400}, Clickable: true, Selected: true},
			{Text: "Settings", ID: id("settings_tab"), Class: "android.widget.TextView", Bounds: [4]int{540, 2240, 1080, 2400}, Clickable: true,
				onClick: func(u *phoneUI) { u.showToast("Settings are not part of the demo") }},
			{Text: "12,500", ID: id("balance"), Desc: "Balance", Class: "android.widget.TextView", Bounds: [4]int{60, 400, 1020, 500}},
		}
		for i := 0; i < 3; i++ {
			ns = append(ns, &node{Text: fmt.Sprintf("Chat %d", i+1), Class: "android.widget.CheckBox", Checkable: true,
				Bounds: [4]int{60, 600 + i*160, 1020, 740 + i*160}, Clickable: true})
		}
		return ns
	}
	return []*node{{Text: "", Class: "android.widget.FrameLayout", Bounds: [4]int{0, 0, screenW, screenH}}}
}

func (u *phoneUI) open(pkg, act string) {
	u.pkg, u.act = pkg, act
	u.focused = ""
}

func (u *phoneUI) showToast(s string) {
	u.toast, u.toastAt = s, time.Now()
}

func (u *phoneUI) addNotification(n map[string]any) {
	u.mu.Lock()
	u.notifs = append(u.notifs, n)
	u.mu.Unlock()
}

func matches(n *node, by, value string) bool {
	switch by {
	case "text":
		return n.Text == value
	case "textContains":
		return value != "" && strings.Contains(n.Text, value)
	case "id":
		return n.ID == value || (!strings.Contains(value, ":") && strings.HasSuffix(n.ID, ":id/"+value))
	case "desc":
		return n.Desc == value
	case "descContains":
		return value != "" && strings.Contains(n.Desc, value)
	case "class":
		return n.Class == value
	}
	return false
}

func (u *phoneUI) find(by, value string, nth int) *node {
	i := 0
	for _, n := range u.nodes() {
		if matches(n, by, value) {
			if i == nth {
				return n
			}
			i++
		}
	}
	return nil
}

func (u *phoneUI) screen() string { return u.pkg + "/" + u.act }

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func num(m map[string]any, k string, def float64) float64 {
	switch v := m[k].(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case float64:
		return v
	}
	return def
}

func ok(fields ...any) map[string]any {
	m := map[string]any{"ok": true}
	for i := 0; i+1 < len(fields); i += 2 {
		m[fields[i].(string)] = fields[i+1]
	}
	return m
}

func fail(code, msg string, fields ...any) map[string]any {
	m := map[string]any{"ok": false, "error": code, "msg": msg}
	for i := 0; i+1 < len(fields); i += 2 {
		m[fields[i].(string)] = fields[i+1]
	}
	return m
}

func (u *phoneUI) notFound(m map[string]any) map[string]any {
	by, val := str(m, "by"), str(m, "value")
	return fail("NOT_FOUND", fmt.Sprintf("could not find %s '%s'", by, val),
		"target", fmt.Sprintf("%s '%s'", by, val), "timeout", num(m, "timeout", 10), "screen", u.screen())
}

// waitFor polls like the real agent; the fake UI only changes on actions, so a
// short wait is enough to exercise the timeout path without slowing tests.
func (u *phoneUI) waitFor(m map[string]any) *node {
	deadline := time.Now().Add(time.Duration(min(num(m, "timeout", 10), 0.3) * float64(time.Second)))
	for {
		u.mu.Lock()
		n := u.find(str(m, "by"), str(m, "value"), int(num(m, "nth", 0)))
		u.mu.Unlock()
		if n != nil || time.Now().After(deadline) {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (u *phoneUI) run(a *Agent, name string, m map[string]any) map[string]any {
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	switch name {
	case "touch", "long_touch":
		n := u.waitFor(m)
		if n == nil {
			u.mu.Lock()
			defer u.mu.Unlock()
			return u.notFound(m)
		}
		u.mu.Lock()
		defer u.mu.Unlock()
		via := "node"
		if !n.Clickable {
			via = "gesture"
		}
		if !n.Disabled && n.onClick != nil {
			n.onClick(u)
		}
		if n.Editable {
			u.focused = n.ID
		}
		return ok("via", via, "ms", ms())
	case "input", "clear":
		n := u.waitFor(m)
		u.mu.Lock()
		defer u.mu.Unlock()
		if n == nil {
			return u.notFound(m)
		}
		if !n.Editable {
			return fail("NOT_CLICKABLE", "element is not editable", "target", str(m, "by")+" '"+str(m, "value")+"'")
		}
		if n.ID == id("email") {
			if name == "clear" {
				u.email = ""
			} else if append, _ := m["append"].(bool); append {
				u.email += str(m, "text")
			} else {
				u.email = str(m, "text")
			}
		}
		u.focused = n.ID
		return ok("via", "set_text", "ms", ms())
	case "wait":
		if u.waitFor(m) == nil {
			u.mu.Lock()
			defer u.mu.Unlock()
			return u.notFound(m)
		}
		return ok("ms", ms())
	case "wait_gone":
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.find(str(m, "by"), str(m, "value"), 0) != nil {
			return fail("TIMEOUT", "still on screen", "cmd", name, "timeout", num(m, "timeout", 10))
		}
		return ok("ms", ms())
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	switch name {
	case "dump":
		return ok("package", u.pkg, "activity", u.act, "width", screenW, "height", screenH, "tree", u.tree())
	case "screenshot":
		data, w, h := u.image(str(m, "format"), int(num(m, "quality", 80)), num(m, "scale", 1))
		return ok("format", str(m, "format"), "width", w, "height", h, "data", data)
	case "current":
		return ok("package", u.pkg, "activity", u.act)
	case "info":
		return ok("model", a.Model, "manufacturer", "droidline", "sdk", 34, "release", "14", "agent", "0.1.0-fake",
			"width", screenW, "height", screenH, "ready", map[string]bool{"a11y": true, "ime": true, "vpn": true, "notif": true, "capture": true})
	case "tap", "long_tap":
		x, y := int(num(m, "x", 0)), int(num(m, "y", 0))
		for _, n := range u.nodes() {
			if x >= n.Bounds[0] && x < n.Bounds[2] && y >= n.Bounds[1] && y < n.Bounds[3] && n.onClick != nil && !n.Disabled {
				n.onClick(u)
				break
			}
		}
		return ok("ms", ms())
	case "swipe":
		return ok("ms", int(num(m, "ms", 300)))
	case "scroll_to":
		if u.find(str(m, "by"), str(m, "value"), int(num(m, "nth", 0))) == nil {
			return u.notFound(m)
		}
		return ok("swipes", 0)
	case "sendkey":
		key := fmt.Sprint(m["key"])
		switch key {
		case "back":
			u.back()
		case "home":
			u.open(homePkg, ".Launcher")
		case "enter", "66":
			if u.pkg == demoPkg && u.act == ".LoginActivity" && u.email != "" {
				u.open(demoPkg, ".MainActivity")
			}
		}
		return ok("via", "ime")
	case "exists":
		return ok("value", u.find(str(m, "by"), str(m, "value"), int(num(m, "nth", 0))) != nil)
	case "get_text":
		if n := u.find(str(m, "by"), str(m, "value"), int(num(m, "nth", 0))); n != nil {
			return ok("value", n.Text)
		}
		return ok("value", "")
	case "checked", "enabled", "selected":
		n := u.find(str(m, "by"), str(m, "value"), int(num(m, "nth", 0)))
		v := n != nil && map[string]bool{"checked": n.Checked, "enabled": !n.Disabled, "selected": n.Selected}[name]
		return ok("value", v)
	case "count":
		c := 0
		for _, n := range u.nodes() {
			if matches(n, str(m, "by"), str(m, "value")) {
				c++
			}
		}
		return ok("value", c)
	case "which":
		cands, _ := m["candidates"].([]any)
		for i, c := range cands {
			pair, _ := c.([]any)
			if len(pair) == 2 && u.find(fmt.Sprint(pair[0]), fmt.Sprint(pair[1]), 0) != nil {
				return ok("value", i)
			}
		}
		return ok("value", -1)
	case "in_app":
		return ok("value", u.pkg == str(m, "package"))
	case "keyboard_shown":
		return ok("value", u.focused != "")
	case "last_toast":
		if time.Since(u.toastAt).Seconds() <= num(m, "max_age", 30) {
			return ok("value", u.toast)
		}
		return ok("value", "")
	case "color":
		return ok("value", "#FFFFFF")
	case "screen_on":
		return ok("value", u.screenOn)
	case "locked":
		return ok("value", !u.screenOn)
	case "wake":
		u.screenOn = true
		return ok("screen_on", true, "locked", false)
	case "lock":
		u.screenOn = false
		return ok()
	case "battery":
		return ok("level", 87, "charging", true, "temperature", 31.5)
	case "orientation":
		return ok("value", "portrait")
	case "network":
		t := "wifi"
		if u.airplane {
			t = "none"
		}
		return ok("type", t, "airplane", u.airplane, "metered", false)
	case "launch":
		switch str(m, "package") {
		case demoPkg:
			act := str(m, "activity")
			if act == "" {
				act = ".LoginActivity"
			}
			if act != ".LoginActivity" && act != ".MainActivity" {
				return fail("ACTIVITY_BLOCKED", act+" is not exported", "activity", act, "package", demoPkg)
			}
			u.open(demoPkg, act)
		case "com.android.chrome":
			u.open("com.android.chrome", "org.chromium.chrome.browser.ChromeTabbedActivity")
		default:
			return fail("APP_NOT_FOUND", "not installed", "package", str(m, "package"), "similar", []string{demoPkg})
		}
		return ok("ms", ms())
	case "open_url", "chrome.go":
		u.open("com.android.chrome", "org.chromium.chrome.browser.ChromeTabbedActivity")
		return ok("ms", ms())
	case "kill", "clear_data":
		if str(m, "package") != demoPkg && str(m, "package") != "com.android.chrome" {
			return fail("APP_NOT_FOUND", "not installed", "package", str(m, "package"), "similar", []string{demoPkg})
		}
		if u.pkg == str(m, "package") {
			u.open(homePkg, ".Launcher")
		}
		if name == "clear_data" && str(m, "package") == demoPkg {
			u.email, u.autoLogin = "", false
		}
		return ok("via", "settings_macro", "ms", 1200)
	case "apps":
		return ok("value", []map[string]any{
			{"package": demoPkg, "label": "Demo", "version": "1.0", "system": false},
			{"package": "com.android.chrome", "label": "Chrome", "version": "129.0", "system": false},
		})
	case "installed":
		switch str(m, "package") {
		case demoPkg:
			return ok("value", "1.0")
		case "com.android.chrome":
			return ok("value", "129.0")
		}
		return ok("value", "")
	case "back":
		u.back()
		return ok()
	case "home":
		u.open(homePkg, ".Launcher")
		return ok()
	case "recents", "open_notifications", "quick_settings":
		return ok()
	case "data", "wifi":
		return ok("via", "settings_macro", "ms", 900)
	case "airplane":
		u.airplane, _ = m["on"].(bool)
		return ok("via", "settings_macro", "ms", 900)
	case "clipboard":
		if t, has := m["text"].(string); has {
			u.clip = t
			return ok("value", t)
		}
		return ok("value", u.clip)
	case "batch":
		return u.batch(a, m)
	case "proxy":
		if m["url"] == nil {
			u.proxyURL, u.proxyApps = "", nil
			return ok("active", false, "apps", []any{})
		}
		u.proxyURL = str(m, "url")
		u.proxyApps, _ = m["app"].([]any)
		return ok("active", true, "apps", u.proxyApps)
	case "proxy_check":
		if u.proxyURL == "" {
			return ok("active", false, "ip", "", "upstream", "", "error", "")
		}
		return ok("active", true, "ip", "203.0.113.7", "upstream", stripCreds(u.proxyURL), "error", "")
	case "notifications":
		out := []map[string]any{}
		for _, n := range u.notifs {
			if p := str(m, "package"); p == "" || p == n["package"] {
				out = append(out, n)
			}
		}
		return ok("value", out)
	case "has_notification":
		match := notifMatch(str(m, "by"), str(m, "value"))
		for _, n := range u.notifs {
			if match(n) {
				return ok("value", true)
			}
		}
		return ok("value", false)
	case "notification_reply", "notification_click", "notification_dismiss":
		for i, n := range u.notifs {
			if n["key"] == str(m, "key") {
				if name != "notification_reply" {
					u.notifs = append(u.notifs[:i], u.notifs[i+1:]...)
				}
				return ok()
			}
		}
		return fail("BAD_ARGS", "no notification with that key", "cmd", name, "reason", "unknown key")
	case "notify_filter":
		if list, has := m["packages"].([]any); has {
			a.mu.Lock()
			a.allow = map[string]bool{}
			for _, p := range list {
				a.allow[fmt.Sprint(p)] = true
			}
			a.mu.Unlock()
		}
		a.mu.Lock()
		out := []string{}
		for p := range a.allow {
			out = append(out, p)
		}
		a.mu.Unlock()
		return ok("value", out)
	}
	return fail("UNKNOWN_CMD", "unknown command "+name, "cmd", name, "agent", "0.1.0-fake")
}

func (u *phoneUI) back() {
	switch {
	case u.pkg == demoPkg && u.act == ".MainActivity":
		u.open(demoPkg, ".LoginActivity")
	default:
		u.open(homePkg, ".Launcher")
	}
}

func (u *phoneUI) batch(a *Agent, m map[string]any) map[string]any {
	steps, _ := m["steps"].([]any)
	stop, _ := m["stop_on_error"].(bool)
	var results []any
	for _, s := range steps {
		st, _ := s.(map[string]any)
		name := str(st, "cmd")
		var r map[string]any
		if name == "sleep" {
			u.mu.Unlock()
			time.Sleep(time.Duration(num(st, "ms", 0)) * time.Millisecond)
			u.mu.Lock()
			r = ok()
		} else {
			u.mu.Unlock()
			r = u.run(a, name, st)
			u.mu.Lock()
		}
		results = append(results, r)
		if okv, _ := r["ok"].(bool); !okv && stop {
			break
		}
	}
	return ok("results", results)
}

func notifMatch(by, val string) func(map[string]any) bool {
	return func(n map[string]any) bool {
		t, _ := n["title"].(string)
		x, _ := n["text"].(string)
		switch by {
		case "text":
			return t == val || x == val
		case "textContains":
			return strings.Contains(t, val) || strings.Contains(x, val)
		case "title":
			return t == val
		case "package":
			return n["package"] == val
		}
		return false
	}
}

func stripCreds(u string) string {
	if at := strings.LastIndex(u, "@"); at >= 0 {
		if s := strings.Index(u, "://"); s >= 0 {
			return u[:s+3] + u[at+1:]
		}
	}
	return u
}

func (u *phoneUI) tree() map[string]any {
	var kids []any
	for _, n := range u.nodes() {
		kids = append(kids, map[string]any{
			"text": n.Text, "id": n.ID, "desc": n.Desc, "class": n.Class, "package": u.pkg,
			"bounds": n.Bounds, "clickable": n.Clickable, "long_clickable": false, "checkable": n.Checkable,
			"checked": n.Checked, "enabled": !n.Disabled, "focused": n.ID != "" && n.ID == u.focused,
			"selected": n.Selected, "scrollable": false, "editable": n.Editable, "password": n.Password,
			"children": []any{},
		})
	}
	return map[string]any{"class": "android.widget.FrameLayout", "package": u.pkg, "bounds": [4]int{0, 0, screenW, screenH},
		"text": "", "id": "", "desc": "", "clickable": false, "enabled": true, "children": kids}
}

// image draws node bounds so a screenshot shows something recognisable.
func (u *phoneUI) image(format string, quality int, scale float64) (string, int, int) {
	w, h := int(float64(screenW)*scale), int(float64(screenH)*scale)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{0xF4, 0xF1, 0xEA, 0xFF}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, bg)
		}
	}
	ink := color.RGBA{0x1F, 0x3A, 0x34, 0xFF}
	for _, n := range u.nodes() {
		b := n.Bounds
		x0, y0, x1, y1 := int(float64(b[0])*scale), int(float64(b[1])*scale), int(float64(b[2])*scale)-1, int(float64(b[3])*scale)-1
		for x := x0; x <= x1; x++ {
			img.Set(x, y0, ink)
			img.Set(x, y1, ink)
		}
		for y := y0; y <= y1; y++ {
			img.Set(x0, y, ink)
			img.Set(x1, y, ink)
		}
	}
	var buf bytes.Buffer
	if format == "png" {
		png.Encode(&buf, img)
	} else {
		jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), w, h
}
