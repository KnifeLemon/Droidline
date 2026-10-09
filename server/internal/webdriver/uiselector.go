package webdriver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// uiSelector is a parsed "-android uiautomator" locator: a Droidline query, an
// optional instance (nth), and for UiScrollable a target to scroll into view.
type uiSelector struct {
	query    map[string]any
	instance int
	scroll   bool // scrollIntoView: scroll until query appears, then find it
}

// parseUiSelector understands the common UiSelector and UiScrollable forms, such as
// new UiSelector().text("OK").clickable(true).instance(1) and
// new UiScrollable(new UiSelector().scrollable(true)).scrollIntoView(new UiSelector().text("About")).
func parseUiSelector(src string) (uiSelector, error) {
	p := &uiParser{s: strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(src), ";"))}
	if p.eat("new UiScrollable(") {
		if _, err := p.selector(); err != nil {
			return uiSelector{}, err
		}
		if !p.eat(")") {
			return uiSelector{}, p.fail("expected ) after the UiScrollable container")
		}
		for p.eat(".") {
			name := p.ident()
			if !p.eat("(") {
				return uiSelector{}, p.fail("expected (")
			}
			if name == "scrollIntoView" || name == "scrollTextIntoView" {
				var target uiSelector
				var err error
				if name == "scrollTextIntoView" {
					text, err := p.str()
					if err != nil {
						return uiSelector{}, err
					}
					target = uiSelector{query: map[string]any{"text": text}}
				} else if target, err = p.selector(); err != nil {
					return uiSelector{}, err
				}
				if !p.eat(")") {
					return uiSelector{}, p.fail("expected )")
				}
				target.scroll = true
				return target, p.end()
			}
			if err := p.skipArgs(); err != nil {
				return uiSelector{}, err
			}
		}
		return uiSelector{}, p.fail("UiScrollable needs scrollIntoView or scrollTextIntoView")
	}
	sel, err := p.selector()
	if err != nil {
		return uiSelector{}, err
	}
	return sel, p.end()
}

type uiParser struct {
	s   string
	pos int
}

func (p *uiParser) fail(msg string) error {
	return fmt.Errorf("-android uiautomator: %s at %q", msg, p.s[min(p.pos, len(p.s)):])
}

func (p *uiParser) space() {
	for p.pos < len(p.s) && strings.ContainsRune(" \t\n\r", rune(p.s[p.pos])) {
		p.pos++
	}
}

func (p *uiParser) eat(tok string) bool {
	p.space()
	if strings.HasPrefix(p.s[p.pos:], tok) {
		p.pos += len(tok)
		return true
	}
	return false
}

func (p *uiParser) end() error {
	p.space()
	if p.pos != len(p.s) {
		return p.fail("unexpected text")
	}
	return nil
}

func (p *uiParser) ident() string {
	p.space()
	start := p.pos
	for p.pos < len(p.s) && (p.s[p.pos] == '_' || p.s[p.pos] >= 'a' && p.s[p.pos] <= 'z' || p.s[p.pos] >= 'A' && p.s[p.pos] <= 'Z' || p.s[p.pos] >= '0' && p.s[p.pos] <= '9') {
		p.pos++
	}
	return p.s[start:p.pos]
}

func (p *uiParser) str() (string, error) {
	p.space()
	if p.pos >= len(p.s) || p.s[p.pos] != '"' {
		return "", p.fail("expected a string")
	}
	var b strings.Builder
	for i := p.pos + 1; i < len(p.s); i++ {
		c := p.s[i]
		if c == '\\' && i+1 < len(p.s) {
			i++
			b.WriteByte(p.s[i])
			continue
		}
		if c == '"' {
			p.pos = i + 1
			return b.String(), nil
		}
		b.WriteByte(c)
	}
	return "", p.fail("unterminated string")
}

func (p *uiParser) skipArgs() error {
	depth := 1
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '"':
			if _, err := p.str(); err != nil {
				return err
			}
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				p.pos++
				return nil
			}
		}
		p.pos++
	}
	return p.fail("unbalanced parentheses")
}

var boolMethods = map[string]string{
	"clickable": "clickable", "checkable": "checkable", "checked": "checked", "enabled": "enabled",
	"focused": "focused", "longClickable": "long_clickable", "scrollable": "scrollable", "selected": "selected",
}

func (p *uiParser) selector() (uiSelector, error) {
	if !p.eat("new UiSelector()") {
		return uiSelector{}, p.fail("expected new UiSelector()")
	}
	sel := uiSelector{query: map[string]any{}}
	for p.eat(".") {
		name := p.ident()
		if !p.eat("(") {
			return sel, p.fail("expected (")
		}
		switch name {
		case "childSelector", "fromParent":
			inner, err := p.selector()
			if err != nil {
				return sel, err
			}
			if !p.eat(")") {
				return sel, p.fail("expected )")
			}
			if parent := sel.query; len(parent) > 0 {
				if name == "childSelector" {
					inner.query["inside"] = parent
				} else {
					inner.query["row"] = parent
				}
			}
			sel = inner
			continue
		}
		p.space()
		var arg any
		if p.pos < len(p.s) && p.s[p.pos] == '"' {
			s, err := p.str()
			if err != nil {
				return sel, err
			}
			arg = s
		} else {
			raw := p.ident()
			if raw == "true" || raw == "false" {
				arg = raw == "true"
			} else if n, err := strconv.Atoi(raw); err == nil {
				arg = n
			} else {
				return sel, p.fail("unsupported argument")
			}
		}
		if !p.eat(")") {
			return sel, p.fail("expected )")
		}
		s, _ := arg.(string)
		switch name {
		case "text", "textContains", "textMatches":
			sel.query[name] = s
		case "textStartsWith":
			sel.query["textMatches"] = regexp.QuoteMeta(s) + ".*"
		case "description":
			sel.query["desc"] = s
		case "descriptionContains":
			sel.query["descContains"] = s
		case "descriptionMatches":
			sel.query["descMatches"] = s
		case "descriptionStartsWith":
			sel.query["descMatches"] = regexp.QuoteMeta(s) + ".*"
		case "resourceId":
			sel.query["id"] = s
		case "className":
			sel.query["class"] = s
		case "packageName":
			sel.query["package"] = s
		case "instance":
			n, _ := arg.(int)
			sel.instance = n
		default:
			key, ok := boolMethods[name]
			b, isBool := arg.(bool)
			if !ok || !isBool {
				return sel, p.fail("UiSelector." + name + " is not supported")
			}
			sel.query[key] = b
		}
	}
	if len(sel.query) == 0 {
		return sel, p.fail("the selector has no conditions")
	}
	return sel, nil
}
