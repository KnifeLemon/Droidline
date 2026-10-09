package webdriver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/KnifeLemon/Droidline/spec"
	"github.com/antchfx/xpath"
)

// xnode is one element of the page source, the XML that Appium Inspector reads
// and XPath runs against. Its attributes follow UiAutomator2, so recorded XPaths carry over.
type xnode struct {
	tag      string
	attrs    [][2]string
	parent   *xnode
	children []*xnode
	src      *spec.Node // nil for the document and the hierarchy element
}

var tagOK = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*$`)

func flag(n *spec.Node, k string) string { return strconv.FormatBool(n.Bool(k)) }

func boundsAttr(b [4]int) string { return fmt.Sprintf("[%d,%d][%d,%d]", b[0], b[1], b[2], b[3]) }

func buildX(n *spec.Node, parent *xnode, index int) *xnode {
	tag := n.Str("class")
	if !tagOK.MatchString(tag) {
		tag = "android.view.View"
	}
	x := &xnode{tag: tag, parent: parent, src: n}
	x.attrs = [][2]string{
		{"index", strconv.Itoa(index)},
		{"package", n.Str("package")},
		{"class", n.Str("class")},
		{"text", n.Str("text")},
		{"resource-id", n.Str("id")},
		{"content-desc", n.Str("desc")},
		{"checkable", flag(n, "checkable")},
		{"checked", flag(n, "checked")},
		{"clickable", flag(n, "clickable")},
		{"enabled", flag(n, "enabled")},
		{"focusable", strconv.FormatBool(n.Bool("clickable") || n.Bool("editable"))},
		{"focused", flag(n, "focused")},
		{"long-clickable", flag(n, "long_clickable")},
		{"password", flag(n, "password")},
		{"scrollable", flag(n, "scrollable")},
		{"selected", flag(n, "selected")},
		{"bounds", boundsAttr(n.Bounds())},
		{"displayed", strconv.FormatBool(n.Visible())},
	}
	for i, c := range n.Children {
		x.children = append(x.children, buildX(c, x, i))
	}
	return x
}

// document turns a dump reply into the page source tree. The document node holds
// one hierarchy element, like UiAutomator2's source.
func document(dump map[string]any) *xnode {
	doc := &xnode{tag: ""}
	w, h := fmt.Sprint(dump["width"]), fmt.Sprint(dump["height"])
	hier := &xnode{tag: "hierarchy", parent: doc, attrs: [][2]string{{"index", "0"}, {"class", "hierarchy"}, {"rotation", "0"}, {"width", w}, {"height", h}}}
	doc.children = []*xnode{hier}
	tree, _ := dump["tree"].(map[string]any)
	if tree == nil {
		return doc
	}
	root := spec.NewTree(tree)
	// The phone wraps several windows in a node of class "windows"; list them side by side.
	if root.Str("class") == "windows" {
		for i, c := range root.Children {
			hier.children = append(hier.children, buildX(c, hier, i))
		}
	} else {
		hier.children = []*xnode{buildX(root, hier, 0)}
	}
	return doc
}

func (x *xnode) xml(b *strings.Builder, depth int) {
	pad := strings.Repeat("  ", depth)
	b.WriteString(pad + "<" + x.tag)
	for _, a := range x.attrs {
		b.WriteString(" " + a[0] + "=\"" + escape(a[1]) + "\"")
	}
	if len(x.children) == 0 {
		b.WriteString(" />\n")
		return
	}
	b.WriteString(">\n")
	for _, c := range x.children {
		c.xml(b, depth+1)
	}
	b.WriteString(pad + "</" + x.tag + ">\n")
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\r", "&#13;", "\t", "&#9;")
	return r.Replace(s)
}

func pageSource(doc *xnode) string {
	var b strings.Builder
	b.WriteString("<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>\n")
	for _, c := range doc.children {
		c.xml(&b, 0)
	}
	return b.String()
}

// find returns the element whose bounds and class equal the snapshot's, for
// XPath that starts from an element.
func (x *xnode) find(fields map[string]any) *xnode {
	if x.src != nil && sameElement(x.src.Fields, fields) {
		return x
	}
	for _, c := range x.children {
		if f := c.find(fields); f != nil {
			return f
		}
	}
	return nil
}

func sameElement(a, b map[string]any) bool {
	return fmt.Sprint(a["bounds"]) == fmt.Sprint(b["bounds"]) && a["class"] == b["class"]
}

// navigator walks xnodes for github.com/antchfx/xpath.
type navigator struct {
	root, cur *xnode
	attr      int // index into cur.attrs, -1 when on the element itself
}

func newNavigator(root, at *xnode) *navigator { return &navigator{root: root, cur: at, attr: -1} }

func (n *navigator) NodeType() xpath.NodeType {
	switch {
	case n.attr >= 0:
		return xpath.AttributeNode
	case n.cur.parent == nil:
		return xpath.RootNode
	}
	return xpath.ElementNode
}

func (n *navigator) LocalName() string {
	if n.attr >= 0 {
		return n.cur.attrs[n.attr][0]
	}
	return n.cur.tag
}

func (n *navigator) Prefix() string { return "" }

func (n *navigator) Value() string {
	if n.attr >= 0 {
		return n.cur.attrs[n.attr][1]
	}
	return ""
}

func (n *navigator) Copy() xpath.NodeNavigator { c := *n; return &c }

func (n *navigator) MoveToRoot() { n.cur, n.attr = n.root, -1 }

func (n *navigator) MoveToParent() bool {
	if n.attr >= 0 {
		n.attr = -1
		return true
	}
	if n.cur.parent == nil {
		return false
	}
	n.cur = n.cur.parent
	return true
}

func (n *navigator) MoveToNextAttribute() bool {
	if n.attr+1 >= len(n.cur.attrs) {
		return false
	}
	n.attr++
	return true
}

func (n *navigator) MoveToChild() bool {
	if n.attr >= 0 || len(n.cur.children) == 0 {
		return false
	}
	n.cur = n.cur.children[0]
	return true
}

func (n *navigator) siblings() ([]*xnode, int) {
	if n.cur.parent == nil {
		return []*xnode{n.cur}, 0
	}
	sib := n.cur.parent.children
	for i, s := range sib {
		if s == n.cur {
			return sib, i
		}
	}
	return sib, -1
}

func (n *navigator) MoveToFirst() bool {
	if n.attr >= 0 {
		return false
	}
	sib, _ := n.siblings()
	n.cur = sib[0]
	return true
}

func (n *navigator) MoveToNext() bool {
	if n.attr >= 0 {
		return false
	}
	sib, i := n.siblings()
	if i < 0 || i+1 >= len(sib) {
		return false
	}
	n.cur = sib[i+1]
	return true
}

func (n *navigator) MoveToPrevious() bool {
	if n.attr >= 0 {
		return false
	}
	sib, i := n.siblings()
	if i <= 0 {
		return false
	}
	n.cur = sib[i-1]
	return true
}

func (n *navigator) MoveTo(other xpath.NodeNavigator) bool {
	o, ok := other.(*navigator)
	if !ok || o.root != n.root {
		return false
	}
	n.cur, n.attr = o.cur, o.attr
	return true
}

// selectXPath runs expr from the context element and returns the matched elements' fields.
func selectXPath(doc, at *xnode, expr string) ([]map[string]any, error) {
	e, err := xpath.Compile(expr)
	if err != nil {
		return nil, err
	}
	it := e.Select(newNavigator(doc, at))
	var out []map[string]any
	for it.MoveNext() {
		nav, ok := it.Current().(*navigator)
		if !ok || nav.attr >= 0 || nav.cur.src == nil {
			continue
		}
		out = append(out, nav.cur.src.Fields)
	}
	return out, nil
}
