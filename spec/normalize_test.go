package spec

import (
	"reflect"
	"strings"
	"testing"
)

func TestSpecLoads(t *testing.T) {
	s := MustLoad()
	if len(s.Commands) < 60 {
		t.Fatalf("only %d commands", len(s.Commands))
	}
	for _, c := range s.Commands {
		if c.Summary["en"] == "" || c.Summary["ko"] == "" || c.Summary["zh"] == "" {
			t.Errorf("%s: summary missing a language", c.Name)
		}
		for _, p := range c.Params {
			if p.Name == "" || p.Type == "" {
				t.Errorf("%s: unresolved param %+v", c.Name, p)
			}
		}
	}
	for _, e := range s.Errors {
		for _, l := range []string{"en", "ko", "zh"} {
			if e.Msg[l] == "" {
				t.Errorf("%s: message missing %s", e.Code, l)
			}
		}
	}
}

func norm(t *testing.T, name string, raw map[string]any) map[string]any {
	t.Helper()
	_, out, err := MustLoad().Normalize(name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func normErr(t *testing.T, name string, raw map[string]any) string {
	t.Helper()
	_, _, err := MustLoad().Normalize(name, raw)
	if err == nil {
		t.Fatalf("%s: expected an error", name)
	}
	return err.Error()
}

func TestAliasAndDefaults(t *testing.T) {
	got := norm(t, "touchById", map[string]any{"cmd": "touchById", "id": 3.0, "value": "login"})
	want := map[string]any{"by": "id", "value": "login", "nth": int64(0), "timeout": float64(10)}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
}

func TestPositionalArgs(t *testing.T) {
	got := norm(t, "tap", map[string]any{"args": []any{540.0, 1200.0}})
	if got["x"] != int64(540) || got["y"] != int64(1200) {
		t.Fatalf("got %v", got)
	}
	got = norm(t, "touchByText", map[string]any{"args": []any{"OK"}})
	if got["by"] != "text" || got["value"] != "OK" {
		t.Fatalf("alias positional: %v", got)
	}
	if !strings.Contains(normErr(t, "tap", map[string]any{"args": []any{1.0, 2.0, 3.0}}), "at most 2") {
		t.Fatal("expected arity error")
	}
}

func TestClientOnlyDropped(t *testing.T) {
	got := norm(t, "screenshot", map[string]any{"path": "a.png", "quality": 50.0})
	if _, ok := got["path"]; ok {
		t.Fatal("path must not reach the phone")
	}
	if got["quality"] != int64(50) || got["format"] != "jpeg" {
		t.Fatalf("got %v", got)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		cmd  string
		raw  map[string]any
		want string
	}{
		{"touch", map[string]any{"by": "xpath", "value": "x"}, "expected one of"},
		{"touch", map[string]any{"by": "id"}, "missing required parameter \"value\""},
		{"touch", map[string]any{"by": "id", "value": "x", "timout": 3.0}, "unknown parameter \"timout\""},
		{"tap", map[string]any{"x": 1.5, "y": 2.0}, "expected an integer"},
		{"swipe", map[string]any{"x1": "sideways"}, "direction must be"},
		{"swipe", map[string]any{"x1": 10.0, "y1": 20.0}, "coordinate swipe needs"},
		{"sendkey", map[string]any{}, "key name"},
		{"proxy", map[string]any{"url": "ftp://x:1", "app": "a"}, "scheme"},
		{"proxy", map[string]any{"url": "socks5://1.2.3.4:1080"}, "app is required"},
		{"screenshot", map[string]any{"quality": 0.0}, "quality"},
		{"sleep", map[string]any{"ms": 10.0}, "batch step"},
	}
	for _, c := range cases {
		if msg := normErr(t, c.cmd, c.raw); !strings.Contains(msg, c.want) {
			t.Errorf("%s %v: %q does not mention %q", c.cmd, c.raw, msg, c.want)
		}
	}
	if e := MustLoad(); e != nil {
		_, _, err := e.Normalize("tuch", nil)
		ae := err.(*ArgError)
		if !ae.Unknown || len(ae.Similar) == 0 || ae.Similar[0] != "touch" {
			t.Errorf("did-you-mean for tuch: %+v", ae)
		}
	}
}

func TestSwipeDirectionAndProxyOff(t *testing.T) {
	got := norm(t, "swipe", map[string]any{"args": []any{"up"}})
	if got["x1"] != "up" || got["ms"] != int64(300) {
		t.Fatalf("got %v", got)
	}
	got = norm(t, "proxy", map[string]any{"url": "off"})
	if got["url"] != nil {
		t.Fatalf("off should clear the proxy: %v", got)
	}
	got = norm(t, "proxy", map[string]any{"url": "socks5://u:p@1.2.3.4:1080", "app": "com.android.chrome"})
	if !reflect.DeepEqual(got["app"], []any{"com.android.chrome"}) {
		t.Fatalf("app should become a list: %v", got["app"])
	}
}

func TestWhichAndBatch(t *testing.T) {
	got := norm(t, "which", map[string]any{"candidates": []any{
		[]any{"text", "로그인"}, map[string]any{"by": "id", "value": "main_tab"},
	}})
	want := []any{[]any{"text", "로그인"}, []any{"id", "main_tab"}}
	if !reflect.DeepEqual(got["candidates"], want) {
		t.Fatalf("candidates %v", got["candidates"])
	}

	s := MustLoad()
	c, got, err := s.Normalize("batch", map[string]any{"steps": []any{
		[]any{"airplane", true}, []any{"sleep", 3000.0}, map[string]any{"cmd": "airplane", "on": false},
	}})
	if err != nil {
		t.Fatal(err)
	}
	steps := got["steps"].([]any)
	if steps[0].(map[string]any)["on"] != true || steps[1].(map[string]any)["ms"] != int64(3000) || steps[2].(map[string]any)["cmd"] != "airplane" {
		t.Fatalf("steps %v", steps)
	}
	if !s.CutsNetwork(c, got) {
		t.Fatal("airplane(true) in a batch cuts the network")
	}
	if !strings.Contains(normErr(t, "batch", map[string]any{"steps": []any{[]any{"devices"}}}), "cannot run inside a batch") {
		t.Fatal("server commands are not batch steps")
	}
}

func TestRenderError(t *testing.T) {
	s := MustLoad()
	msg := s.RenderError("NOT_FOUND", "ko", map[string]any{"by": "id", "value": "com.kakao.talk:id/login", "timeout": 10.0, "screen": "com.kakao.talk / .LoginActivity"})
	want := "10초 동안 id 'com.kakao.talk:id/login' 대상을 찾지 못했습니다. 현재 화면: com.kakao.talk / .LoginActivity"
	if msg != want {
		t.Fatalf("got  %q\nwant %q", msg, want)
	}
	if got := s.RenderError("NO_IME", "zh", map[string]any{}); !strings.Contains(got, "Droidline 键盘") {
		t.Fatalf("zh: %q", got)
	}
}
