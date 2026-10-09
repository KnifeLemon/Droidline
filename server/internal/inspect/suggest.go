// Package inspect serves droidline inspect: a local page that shows a phone's screen
// and element tree, suggests selectors with code, and records taps and typing as a
// script. It is optional and runs only while the command runs.
package inspect

import (
	"fmt"
	"strings"

	"github.com/KnifeLemon/Droidline/spec"
)

// Selector is one way to find an element, with how many elements it matches and
// which of them is the one wanted.
type Selector struct {
	By    any    `json:"by"` // a selector name, or a query object
	Value string `json:"value,omitempty"`
	Nth   int    `json:"nth"`
	Count int    `json:"count"`
}

func (s Selector) key() string { return fmt.Sprintf("%v|%s|%d", s.By, s.Value, s.Nth) }

// Suggest lists selectors for target, the ones that match only it first. The order
// within each group prefers what survives app updates: id, then text, then
// description, then combinations and relations, and a class with nth last.
func Suggest(roots []*spec.Node, target *spec.Node) []Selector {
	var unique, shared []Selector
	seen := map[string]bool{}
	try := func(by any, value string) {
		found := spec.FindAll(roots, by, value)
		nth := -1
		for i, n := range found {
			if n == target {
				nth = i
			}
		}
		if nth < 0 {
			return
		}
		s := Selector{By: by, Value: value, Nth: nth, Count: len(found)}
		if seen[s.key()] {
			return
		}
		seen[s.key()] = true
		if len(found) == 1 {
			unique = append(unique, s)
		} else {
			shared = append(shared, s)
		}
	}

	id, text, desc, cls := target.Str("id"), target.Str("text"), target.Str("desc"), target.Str("class")
	if id != "" {
		if _, name, ok := strings.Cut(id, ":id/"); ok {
			try("id", name)
		}
		try("id", id)
	}
	if text != "" && !target.Bool("password") {
		try("text", text)
	}
	if desc != "" {
		try("desc", desc)
	}
	if cls != "" && text != "" && !target.Bool("password") {
		try(map[string]any{"class": cls, "text": text}, "")
	}
	if cls != "" && desc != "" {
		try(map[string]any{"class": cls, "desc": desc}, "")
	}
	if inner := firstText(target, true); inner != "" && text == "" {
		q := map[string]any{"has": map[string]any{"text": inner}}
		if target.Bool("clickable") {
			q["clickable"] = true
		}
		try(q, "")
	}
	if row := spec.RowOf(target); row != target && cls != "" {
		if label := firstTextExcept(row, target); label != "" {
			try(map[string]any{"class": cls, "row": map[string]any{"text": label}}, "")
		}
	}
	if target.Bool("editable") && cls != "" {
		try(map[string]any{"class": cls, "editable": true}, "")
	}
	if cls != "" {
		try("class", cls)
	}
	out := append(unique, shared...)
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

// firstText is the first non-empty text inside n (n itself counts when self is true).
func firstText(n *spec.Node, self bool) string {
	found := ""
	n.Walk(func(d *spec.Node) {
		if found == "" && (self || d != n) && !d.Bool("password") {
			found = d.Str("text")
		}
	})
	return found
}

func firstTextExcept(row, skip *spec.Node) string {
	found := ""
	row.Walk(func(d *spec.Node) {
		if found != "" || d == skip || within(d, skip) || d.Bool("password") {
			return
		}
		found = d.Str("text")
	})
	return found
}

func within(n, ancestor *spec.Node) bool {
	for a := n.Parent; a != nil; a = a.Parent {
		if a == ancestor {
			return true
		}
	}
	return false
}

// At returns the smallest element that contains the point, the one a tap there hits.
func At(roots []*spec.Node, x, y int) *spec.Node {
	var best *spec.Node
	bestArea := -1
	for _, r := range roots {
		r.Walk(func(n *spec.Node) {
			b := n.Bounds()
			if x < b[0] || x >= b[2] || y < b[1] || y >= b[3] {
				return
			}
			if a := (b[2] - b[0]) * (b[3] - b[1]); bestArea < 0 || a <= bestArea {
				best, bestArea = n, a
			}
		})
	}
	return best
}
