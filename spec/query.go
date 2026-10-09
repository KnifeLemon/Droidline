package spec

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// A query is a selector written as an object: several conditions on one element,
// all of which must hold. The phone app implements the same rules in Kotlin;
// spec/query-vectors.json keeps the two in step.

var queryStringKeys = []string{"text", "textContains", "textMatches", "id", "desc", "descContains", "descMatches", "class", "package"}
var queryBoolKeys = []string{"clickable", "long_clickable", "checkable", "checked", "enabled", "focused", "selected", "scrollable", "editable", "password", "visible"}

// Relations take another query. The four directions also order the matches, nearest first.
var queryRelationKeys = []string{"has", "inside", "row", "below", "above", "left_of", "right_of"}
var queryDirections = []string{"below", "above", "left_of", "right_of"}

const queryMaxDepth = 8

// QueryKeys lists every key a query accepts, for error messages and docs.
func QueryKeys() []string {
	out := append([]string{}, queryStringKeys...)
	out = append(out, queryBoolKeys...)
	out = append(out, "bounds")
	return append(out, queryRelationKeys...)
}

// CheckQuery validates a query object and returns a copy with bounds as int64.
func CheckQuery(v any) (map[string]any, error) { return checkQuery(v, 0) }

func checkQuery(v any, depth int) (map[string]any, error) {
	if depth > queryMaxDepth {
		return nil, fmt.Errorf("query is nested more than %d levels", queryMaxDepth)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a query must be an object")
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("a query needs at least one condition")
	}
	out := map[string]any{}
	for k, val := range m {
		switch {
		case contains(queryStringKeys, k):
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string", k)
			}
			out[k] = s
		case contains(queryBoolKeys, k):
			b, ok := val.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be true or false", k)
			}
			out[k] = b
		case k == "bounds":
			list, ok := val.([]any)
			if !ok || len(list) != 4 {
				return nil, fmt.Errorf("bounds must be [left, top, right, bottom]")
			}
			b := make([]any, 4)
			for i, x := range list {
				n, ok := asInt(x)
				if !ok {
					return nil, fmt.Errorf("bounds must be four integers")
				}
				b[i] = n
			}
			out[k] = b
		case contains(queryRelationKeys, k):
			sub, err := checkQuery(val, depth+1)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = sub
		default:
			return nil, fmt.Errorf("unknown query key %q (accepted: %s)", k, strings.Join(QueryKeys(), ", "))
		}
	}
	return out, nil
}

// DescribeQuery is the target text used in NOT_FOUND and friends.
func DescribeQuery(q map[string]any) string {
	b, _ := json.Marshal(q)
	return string(b)
}

// Node is one element of a dump tree, linked to its parent.
type Node struct {
	Fields   map[string]any
	Parent   *Node
	Children []*Node
}

// NewTree links a dump tree (the "tree" field of dump) into Nodes.
func NewTree(m map[string]any) *Node { return newNode(m, nil) }

func newNode(m map[string]any, parent *Node) *Node {
	n := &Node{Fields: map[string]any{}, Parent: parent}
	for k, v := range m {
		if k != "children" {
			n.Fields[k] = v
		}
	}
	if kids, ok := m["children"].([]any); ok {
		for _, k := range kids {
			if km, ok := k.(map[string]any); ok {
				n.Children = append(n.Children, newNode(km, n))
			}
		}
	}
	return n
}

// Walk visits the node and its descendants in document order.
func (n *Node) Walk(fn func(*Node)) {
	fn(n)
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

func (n *Node) Str(k string) string { s, _ := n.Fields[k].(string); return s }
func (n *Node) Bool(k string) bool  { b, _ := n.Fields[k].(bool); return b }

// Visible reports whether the element is on screen. Phones that predate the field send none; those count as visible.
func (n *Node) Visible() bool {
	v, ok := n.Fields["visible"].(bool)
	return !ok || v
}

// Bounds returns left, top, right, bottom.
func (n *Node) Bounds() [4]int {
	var out [4]int
	switch b := n.Fields["bounds"].(type) {
	case [4]int:
		return b
	case []any:
		for i := 0; i < 4 && i < len(b); i++ {
			if v, ok := asInt(b[i]); ok {
				out[i] = int(v)
			}
		}
	}
	return out
}

func (n *Node) center() (float64, float64) {
	b := n.Bounds()
	return float64(b[0]+b[2]) / 2, float64(b[1]+b[3]) / 2
}

func (n *Node) emptyBounds() bool {
	b := n.Bounds()
	return b[2] <= b[0] || b[3] <= b[1]
}

// MatchField applies one simple selector (by, value) to a node.
func MatchField(by, value string, n *Node) bool {
	switch by {
	case "text":
		return n.Str("text") == value
	case "textContains":
		return strings.Contains(n.Str("text"), value)
	case "textMatches":
		return fullMatch(value, n.Str("text"))
	case "id":
		id := n.Str("id")
		return id == value || (!strings.Contains(value, ":") && strings.HasSuffix(id, ":id/"+value))
	case "desc":
		return n.Str("desc") == value
	case "descContains":
		return strings.Contains(n.Str("desc"), value)
	case "descMatches":
		return fullMatch(value, n.Str("desc"))
	case "class":
		return n.Str("class") == value
	case "package":
		return n.Str("package") == value
	}
	return false
}

func fullMatch(pattern, s string) bool {
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	return err == nil && re.MatchString(s)
}

// FindAll returns the nodes a selector matches: by is a selector name with a
// value, or a query object. Directional queries come back nearest first.
func FindAll(roots []*Node, by any, value string) []*Node {
	var out []*Node
	if s, ok := by.(string); ok {
		for _, r := range roots {
			r.Walk(func(n *Node) {
				if MatchField(s, value, n) {
					out = append(out, n)
				}
			})
		}
		return out
	}
	q, _ := by.(map[string]any)
	return findQuery(roots, q)
}

func findQuery(roots []*Node, q map[string]any) []*Node {
	anchors := map[string]*Node{}
	for _, dir := range queryDirections {
		if sub, ok := q[dir].(map[string]any); ok {
			a := findQuery(roots, sub)
			if len(a) == 0 {
				return nil
			}
			anchors[dir] = a[0]
		}
	}
	var out []*Node
	for _, r := range roots {
		r.Walk(func(n *Node) {
			if matchQuery(roots, q, n, anchors) {
				out = append(out, n)
			}
		})
	}
	for _, dir := range queryDirections {
		if a := anchors[dir]; a != nil {
			ax, ay := a.center()
			sort.SliceStable(out, func(i, j int) bool {
				return dist(out[i], ax, ay) < dist(out[j], ax, ay)
			})
			break
		}
	}
	return out
}

func dist(n *Node, x, y float64) float64 {
	cx, cy := n.center()
	return math.Hypot(cx-x, cy-y)
}

func matchQuery(roots []*Node, q map[string]any, n *Node, anchors map[string]*Node) bool {
	for k, v := range q {
		switch {
		case contains(queryStringKeys, k):
			if !MatchField(k, v.(string), n) {
				return false
			}
		case k == "visible":
			if n.Visible() != v.(bool) {
				return false
			}
		case contains(queryBoolKeys, k):
			if n.Bool(k) != v.(bool) {
				return false
			}
		case k == "bounds":
			b := n.Bounds()
			for i, x := range v.([]any) {
				if xi, _ := asInt(x); int(xi) != b[i] {
					return false
				}
			}
		case k == "has":
			if !subtreeHas(roots, n, v.(map[string]any), false) {
				return false
			}
		case k == "row":
			if !subtreeHas(roots, rowOf(n), v.(map[string]any), true) {
				return false
			}
		case k == "inside":
			found := false
			sub := v.(map[string]any)
			for a := n.Parent; a != nil && !found; a = a.Parent {
				found = matchQuery(roots, sub, a, subAnchors(roots, sub))
			}
			if !found {
				return false
			}
		default:
			a := anchors[k]
			if a == nil || n.emptyBounds() || n == a {
				return false
			}
			cx, cy := n.center()
			ab := a.Bounds()
			ok := false
			switch k {
			case "below":
				ok = cy > float64(ab[3])
			case "above":
				ok = cy < float64(ab[1])
			case "left_of":
				ok = cx < float64(ab[0])
			case "right_of":
				ok = cx > float64(ab[2])
			}
			if !ok {
				return false
			}
		}
	}
	return true
}

// subtreeHas reports whether a descendant of root (or root itself, when self is
// true) matches the query.
func subtreeHas(roots []*Node, root *Node, q map[string]any, self bool) bool {
	anchors := subAnchors(roots, q)
	found := false
	root.Walk(func(d *Node) {
		if found || (d == root && !self) {
			return
		}
		found = matchQuery(roots, q, d, anchors)
	})
	return found
}

func subAnchors(roots []*Node, q map[string]any) map[string]*Node {
	anchors := map[string]*Node{}
	for _, dir := range queryDirections {
		if sub, ok := q[dir].(map[string]any); ok {
			if a := findQuery(roots, sub); len(a) > 0 {
				anchors[dir] = a[0]
			}
		}
	}
	return anchors
}

// RowOf is the row the row key compares against; the inspector uses it to suggest selectors.
func RowOf(n *Node) *Node { return rowOf(n) }

// rowOf is the list item holding n. Outside a list it is the nearest clickable
// ancestor, else the parent.
func rowOf(n *Node) *Node {
	for a := n; a.Parent != nil; a = a.Parent {
		if isList(a.Parent) {
			return a
		}
	}
	for a := n.Parent; a != nil; a = a.Parent {
		if a.Bool("clickable") {
			return a
		}
	}
	if n.Parent != nil {
		return n.Parent
	}
	return n
}

// isList: a list widget, or a scrollable view with several children. A short list
// that fits on screen is not scrollable, so the class counts too.
func isList(n *Node) bool {
	cls := n.Str("class")
	for _, s := range []string{"RecyclerView", "ListView", "GridView"} {
		if strings.HasSuffix(cls, s) {
			return true
		}
	}
	return n.Bool("scrollable") && len(n.Children) > 1
}
