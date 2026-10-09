package webdriver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// caps merges W3C alwaysMatch with the first firstMatch entry, plus legacy desiredCapabilities.
func caps(body map[string]any) map[string]any {
	out := map[string]any{}
	if d, ok := body["desiredCapabilities"].(map[string]any); ok {
		for k, v := range d {
			out[k] = v
		}
	}
	c, _ := body["capabilities"].(map[string]any)
	if a, ok := c["alwaysMatch"].(map[string]any); ok {
		for k, v := range a {
			out[k] = v
		}
	}
	if list, ok := c["firstMatch"].([]any); ok && len(list) > 0 {
		if f, ok := list[0].(map[string]any); ok {
			for k, v := range f {
				out[k] = v
			}
		}
	}
	return out
}

func capStr(c map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := c[k].(string); ok && v != "" {
			return v
		}
		if v, ok := c["appium:"+k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// newSession picks the phone: droidline:device or udid strictly, deviceName only if a
// phone has that name or ID (Appium setups often put any text there). droidline:lease
// borrows a free phone for the session. appPackage and appActivity launch the app;
// nothing is reset or cleared unless you ask for it.
func (b *Bridge) newSession(_ *http.Request, _ *session, body map[string]any) (any, error) {
	c := caps(body)
	if p := capStr(c, "platformName"); p != "" && !strings.EqualFold(p, "android") {
		return nil, errf(500, "session not created", "Droidline drives Android phones; platformName was %s", p)
	}
	devs, err := b.cl.Call(map[string]any{"cmd": "devices"})
	if err != nil {
		return nil, errf(500, "session not created", "%v", err)
	}
	list, _ := devs["value"].([]any)
	known := func(ref string) bool {
		for _, d := range list {
			m, _ := d.(map[string]any)
			if m["id"] == ref || m["name"] == ref {
				return true
			}
		}
		return false
	}
	s := &session{id: newID(), elements: map[string]map[string]any{}, idle: 60 * time.Second, last: time.Now()}
	if v, ok := c["appium:newCommandTimeout"].(float64); ok && v >= 0 {
		s.idle = time.Duration(v * float64(time.Second))
	}
	if ref := capStr(c, "droidline:device", "udid"); ref != "" {
		if !known(ref) {
			return nil, errf(500, "session not created", "no paired phone is called %s", ref)
		}
		s.device = ref
	} else if ref := capStr(c, "deviceName"); ref != "" && known(ref) {
		s.device = ref
	}
	if lease, _ := c["droidline:lease"].(bool); lease {
		req := map[string]any{"cmd": "lease"}
		if s.device != "" {
			req["device"] = s.device
		}
		res, err := b.cl.Call(req)
		if err != nil {
			return nil, errf(500, "session not created", "%v", err)
		}
		if ok, _ := res["ok"].(bool); !ok {
			return nil, errf(500, "session not created", "%v", res["msg"])
		}
		s.device, _ = res["device"].(string)
		s.lease, _ = res["lease"].(string)
	}
	if pkg := capStr(c, "appPackage"); pkg != "" {
		req := map[string]any{"cmd": "launch", "package": pkg}
		if act := capStr(c, "appActivity"); act != "" {
			req["activity"] = act
		}
		if _, err := b.call(s, req); err != nil {
			b.releaseSession(s)
			return nil, errf(500, "session not created", "%v", err)
		}
	}
	info, err := b.call(s, map[string]any{"cmd": "info"})
	if err != nil {
		b.releaseSession(s)
		return nil, errf(500, "session not created", "%v", err)
	}
	s.caps = map[string]any{}
	for k, v := range c {
		s.caps[k] = v
	}
	s.caps["platformName"] = "Android"
	s.caps["appium:automationName"] = "Droidline"
	s.caps["appium:platformVersion"] = fmt.Sprint(info["release"])
	s.caps["appium:deviceModel"] = info["model"]
	if s.device != "" {
		s.caps["appium:udid"] = s.device
	}
	b.mu.Lock()
	b.sessions[s.id] = s
	b.mu.Unlock()
	return map[string]any{"sessionId": s.id, "capabilities": s.caps}, nil
}

func (b *Bridge) releaseSession(s *session) {
	if s.lease != "" {
		_, _ = b.cl.Call(map[string]any{"cmd": "release", "lease": s.lease})
	}
}

func (b *Bridge) deleteSession(_ *http.Request, s *session, _ map[string]any) (any, error) {
	b.mu.Lock()
	delete(b.sessions, s.id)
	b.mu.Unlock()
	b.releaseSession(s)
	return nil, nil
}
