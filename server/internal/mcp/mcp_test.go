package mcp

import (
	"testing"

	"github.com/KnifeLemon/Droidline/spec"
)

func names(t *testing.T, o Options) map[string]bool {
	t.Helper()
	sp := spec.MustLoad()
	allow, err := allowed(sp, o)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{sp: sp, allow: allow}
	out := map[string]bool{}
	for _, tool := range s.tools() {
		out[tool.(map[string]any)["name"].(string)] = true
	}
	return out
}

func TestDefaultListsEveryToolButOptIn(t *testing.T) {
	got := names(t, Options{})
	want := 0
	for _, c := range spec.MustLoad().Commands {
		if c.InMCP() && !c.MCPOptIn {
			want++
		}
	}
	if len(got) != want {
		t.Fatalf("listed %d tools, want %d", len(got), want)
	}
}

func TestToolsAllowlist(t *testing.T) {
	got := names(t, Options{Tools: []string{"dump", " touch", "chrome.go"}})
	if len(got) != 3 || !got["dump"] || !got["touch"] || !got["chrome_go"] {
		t.Fatalf("got %v", got)
	}
}

func TestToolsUnknownName(t *testing.T) {
	if _, err := allowed(spec.MustLoad(), Options{Tools: []string{"dump", "tuoch"}}); err == nil {
		t.Fatal("want an error for a misspelled tool")
	}
}

func TestReadOnlyDropsActions(t *testing.T) {
	got := names(t, Options{ReadOnly: true})
	for _, n := range []string{"dump", "screenshot", "exists", "get_text", "notifications", "devices"} {
		if !got[n] {
			t.Errorf("%s missing from --read-only", n)
		}
	}
	for _, n := range []string{"touch", "tap", "input", "tap_image", "ocr_tap", "launch", "intent", "batch", "clear_data", "notification_reply", "notify_filter", "wake", "clipboard"} {
		if got[n] {
			t.Errorf("%s listed under --read-only", n)
		}
	}
}

func TestReadOnlyAndToolsIntersect(t *testing.T) {
	got := names(t, Options{Tools: []string{"dump", "touch"}, ReadOnly: true})
	if len(got) != 1 || !got["dump"] {
		t.Fatalf("got %v", got)
	}
}

func TestCallOutsideTheSetIsRefused(t *testing.T) {
	sp := spec.MustLoad()
	allow, _ := allowed(sp, Options{Tools: []string{"dump"}})
	s := &session{sp: sp, allow: allow}
	res := s.callTool("touch", map[string]any{"by": "text", "value": "Send"})
	if res["isError"] != true {
		t.Fatalf("touch was not refused: %v", res)
	}
}

func TestAnnotationsMarkTapsAsActions(t *testing.T) {
	sp := spec.MustLoad()
	allow, _ := allowed(sp, Options{})
	s := &session{sp: sp, allow: allow}
	for _, tool := range s.tools() {
		m := tool.(map[string]any)
		ro := m["annotations"].(map[string]any)["readOnlyHint"].(bool)
		switch m["name"] {
		case "tap_image", "ocr_tap", "touch":
			if ro {
				t.Errorf("%s is marked read-only", m["name"])
			}
		case "dump", "ocr":
			if !ro {
				t.Errorf("%s is not marked read-only", m["name"])
			}
		}
	}
}

func TestOptInToolNeedsTools(t *testing.T) {
	if got := names(t, Options{}); got["sms"] || got["calls"] || got["contacts"] {
		t.Fatal("an opt-in tool is listed without --tools")
	}
	if got := names(t, Options{ReadOnly: true}); got["sms"] {
		t.Fatal("sms listed under --read-only alone")
	}
	if got := names(t, Options{Tools: []string{"sms", "dump"}, ReadOnly: true}); !got["sms"] || !got["dump"] {
		t.Fatalf("got %v", got)
	}
	sp := spec.MustLoad()
	allow, _ := allowed(sp, Options{})
	s := &session{sp: sp, allow: allow}
	if res := s.callTool("sms", map[string]any{}); res["isError"] != true {
		t.Fatalf("sms was not refused: %v", res)
	}
}

func TestBatchStepsMustBeAllowed(t *testing.T) {
	sp := spec.MustLoad()
	allow, _ := allowed(sp, Options{Tools: []string{"batch", "touch"}})
	s := &session{sp: sp, allow: allow}
	for _, steps := range [][]any{
		{[]any{"touch", "text", "OK"}, []any{"clear_data", "com.android.chrome"}},
		{map[string]any{"cmd": "sms"}},
		{[]any{"touchByText", "OK"}, []any{"sleep", 100.0}, map[string]any{"cmd": "tap", "x": 1.0, "y": 2.0}},
	} {
		if res := s.callTool("batch", map[string]any{"steps": steps}); res["isError"] != true {
			t.Fatalf("batch %v was not refused: %v", steps, res)
		}
	}
	allow, _ = allowed(sp, Options{})
	s = &session{sp: sp, allow: allow}
	if res := s.callTool("batch", map[string]any{"steps": []any{[]any{"sms"}}}); res["isError"] != true {
		t.Fatalf("default set let sms run inside batch: %v", res)
	}
}
