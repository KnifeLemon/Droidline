package inspect

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/KnifeLemon/Droidline/spec"
)

func vectorsTree(t *testing.T) map[string]any {
	raw, err := os.ReadFile("../../../spec/query-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Tree map[string]any `json:"tree"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Tree
}

func TestSuggestPrefersUniqueAndStable(t *testing.T) {
	roots := []*spec.Node{spec.NewTree(vectorsTree(t))}
	at := func(x, y int) *spec.Node { return At(roots, x, y) }

	// The title id is shared by three rows, so the text comes first.
	got := Suggest(roots, at(100, 350))
	if got[0].By != "text" || got[0].Value != "Wi-Fi" || got[0].Count != 1 {
		t.Fatalf("Wi-Fi title: %+v", got[0])
	}
	if last := got[len(got)-1]; last.Count == 1 {
		t.Fatalf("shared selectors should come last: %+v", got)
	}

	// A switch has no text of its own; its row names it.
	sw := Suggest(roots, at(950, 600))
	if q, ok := sw[0].By.(map[string]any); !ok || sw[0].Count != 1 || q["row"] == nil {
		t.Fatalf("Bluetooth switch: %+v", sw[0])
	}

	// A row without text is found by what it holds.
	row := Suggest(roots, at(850, 320))
	if q, ok := row[0].By.(map[string]any); !ok || q["has"] == nil {
		t.Fatalf("Wi-Fi row: %+v", row[0])
	}

	// Two buttons share a class; the class selector carries nth.
	for _, s := range Suggest(roots, at(800, 2300)) {
		if s.By == "class" && (s.Nth != 1 || s.Count != 2) {
			t.Fatalf("class with nth: %+v", s)
		}
	}
}

func TestCodeInEachLanguage(t *testing.T) {
	text := Step{Cmd: "touch", Sel: &Selector{By: "text", Value: "로그인"}}
	query := Step{Cmd: "input", Sel: &Selector{By: map[string]any{"editable": true, "below": map[string]any{"text": "이메일"}}}, Text: "knife"}
	nth := Step{Cmd: "long_touch", Sel: &Selector{By: "class", Value: "android.widget.Button", Nth: 1}}
	cases := map[string][3]string{
		"python": {`d.touch("text", "로그인")`, `d.input({"below": {"text": "이메일"}, "editable": True}, "knife")`, `d.long_touch("class", "android.widget.Button", nth=1)`},
		"node":   {`await d.touch("text", "로그인");`, `await d.input({ below: { text: "이메일" }, editable: true }, "knife");`, `await d.longTouch("class", "android.widget.Button", { nth: 1 });`},
		"cli":    {`droidline touch text '로그인'`, `droidline input '{"below":{"text":"이메일"},"editable":true}' knife`, `droidline long_touch class android.widget.Button --nth 1`},
	}
	for lang, want := range cases {
		for i, s := range []Step{text, query, nth} {
			if got := s.Code(lang); got != want[i] {
				t.Errorf("%s step %d:\n got %s\nwant %s", lang, i, got, want[i])
			}
		}
	}
	pw := Step{Cmd: "input", Sel: &Selector{By: "id", Value: "password"}, Password: true, Text: "secret"}
	if s := Script("python", "shelf-01", []Step{{Cmd: "launch", Package: "com.example"}, pw}); !strings.Contains(s, `connect("shelf-01")`) ||
		!strings.Contains(s, passwordPlaceholder) || strings.Contains(s, "secret") {
		t.Fatalf("script:\n%s", s)
	}
}

func TestRecordedActionsMerge(t *testing.T) {
	tree := vectorsTree(t)
	screen := map[string]any{"tree": tree}
	field := map[string]any{"bounds": []any{40.0, 160.0, 1040.0, 260.0}, "class": "android.widget.EditText"}
	var steps []Step
	steps = applyAction(steps, map[string]any{"kind": "click", "target": field, "screen": screen})
	steps = applyAction(steps, map[string]any{"kind": "input", "target": field, "screen": screen, "text": "w"})
	steps = applyAction(steps, map[string]any{"kind": "input", "target": field, "text": "wifi"})
	if len(steps) != 1 || steps[0].Cmd != "input" || steps[0].Text != "wifi" {
		t.Fatalf("typing should replace the tap and update one step: %+v", steps)
	}
	button := map[string]any{"bounds": []any{600.0, 2200.0, 1000.0, 2350.0}, "class": "android.widget.Button"}
	steps = applyAction(steps, map[string]any{"kind": "click", "target": button, "screen": screen})
	if len(steps) != 2 || steps[1].Code("python") != `d.touch("text", "확인")` {
		t.Fatalf("button tap: %+v", steps)
	}
}

func TestSnapshotNumbersNodesInOrder(t *testing.T) {
	tree := vectorsTree(t)
	nodes := numberTree(tree)
	seen := 0
	var walk func(m map[string]any)
	walk = func(m map[string]any) {
		if m["_i"] != seen {
			t.Fatalf("node %v has _i %v, want %d", m["class"], m["_i"], seen)
		}
		if nodes[seen].Str("class") != m["class"] {
			t.Fatalf("node %d is %v in the list but %v in the tree", seen, nodes[seen].Str("class"), m["class"])
		}
		seen++
		kids, _ := m["children"].([]any)
		for _, k := range kids {
			walk(k.(map[string]any))
		}
	}
	walk(tree)
}

func TestRecordedTapAfterTheScreenChanged(t *testing.T) {
	screen := map[string]any{"tree": vectorsTree(t)}
	// A row that is not on the screen that was read: the selector comes from the event itself.
	gone := map[string]any{"bounds": []any{0.0, 113.0, 800.0, 227.0}, "class": "android.widget.LinearLayout", "inner": "네트워크 및 인터넷"}
	steps := applyAction(nil, map[string]any{"kind": "click", "target": gone, "screen": screen})
	if len(steps) != 1 || steps[0].Code("python") != `d.touch({"clickable": True, "has": {"text": "네트워크 및 인터넷"}})` {
		t.Fatalf("fallback: %+v", steps)
	}
}
