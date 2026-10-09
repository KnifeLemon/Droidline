package inspect

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Step is one line of a recorded or inspected script.
type Step struct {
	Cmd      string    `json:"cmd"` // launch, touch, long_touch, input, get_text
	Sel      *Selector `json:"sel,omitempty"`
	Text     string    `json:"text,omitempty"`
	Package  string    `json:"package,omitempty"`
	Password bool      `json:"password,omitempty"`

	at [4]int // bounds of the element when recorded, to merge typing into one step
}

// The recorder never sees password text; the script says where to put it.
const passwordPlaceholder = "<password>"

func (s Step) text() string {
	if s.Password {
		return passwordPlaceholder
	}
	return s.Text
}

// Code renders the step in "python", "node" or "cli".
func (s Step) Code(lang string) string {
	if s.Cmd == "launch" {
		switch lang {
		case "python":
			return fmt.Sprintf("d.launch(%s)", strconv.Quote(s.Package))
		case "node":
			return fmt.Sprintf("await d.launch(%s);", strconv.Quote(s.Package))
		}
		return "droidline launch " + shellQuote(s.Package)
	}
	if s.Sel == nil {
		return ""
	}
	sel := *s.Sel
	_, isQuery := sel.By.(map[string]any)
	switch lang {
	case "python":
		args := []string{}
		if isQuery {
			args = append(args, pyLiteral(sel.By))
		} else {
			args = append(args, strconv.Quote(fmt.Sprint(sel.By)), strconv.Quote(sel.Value))
		}
		if s.Cmd == "input" {
			args = append(args, strconv.Quote(s.text()))
		}
		if sel.Nth > 0 {
			args = append(args, fmt.Sprintf("nth=%d", sel.Nth))
		}
		return fmt.Sprintf("d.%s(%s)", s.Cmd, strings.Join(args, ", "))
	case "node":
		args := []string{}
		if isQuery {
			args = append(args, jsLiteral(sel.By))
		} else {
			args = append(args, strconv.Quote(fmt.Sprint(sel.By)), strconv.Quote(sel.Value))
		}
		if s.Cmd == "input" {
			args = append(args, strconv.Quote(s.text()))
		}
		if sel.Nth > 0 {
			args = append(args, fmt.Sprintf("{ nth: %d }", sel.Nth))
		}
		return fmt.Sprintf("await d.%s(%s);", camel(s.Cmd), strings.Join(args, ", "))
	}
	parts := []string{"droidline", s.Cmd}
	if isQuery {
		b, _ := json.Marshal(sel.By)
		parts = append(parts, "'"+strings.ReplaceAll(string(b), "'", `'\''`)+"'")
	} else {
		parts = append(parts, fmt.Sprint(sel.By), shellQuote(sel.Value))
	}
	if s.Cmd == "input" {
		parts = append(parts, shellQuote(s.text()))
	}
	if sel.Nth > 0 {
		parts = append(parts, fmt.Sprintf("--nth %d", sel.Nth))
	}
	return strings.Join(parts, " ")
}

// Script wraps steps into a file you can run as is.
func Script(lang, device string, steps []Step) string {
	var b strings.Builder
	hasPassword := false
	for _, s := range steps {
		hasPassword = hasPassword || s.Password
	}
	switch lang {
	case "python":
		b.WriteString("from droidline import connect\n\n")
		if device != "" {
			fmt.Fprintf(&b, "d = connect(%s)\n", strconv.Quote(device))
		} else {
			b.WriteString("d = connect()\n")
		}
		if hasPassword {
			b.WriteString("# Replace <password>: the recorder does not see what you type into password fields.\n")
		}
	case "node":
		b.WriteString("import { connect } from \"droidline\";\n\n")
		if device != "" {
			fmt.Fprintf(&b, "const d = await connect(%s);\n", strconv.Quote(device))
		} else {
			b.WriteString("const d = await connect();\n")
		}
		if hasPassword {
			b.WriteString("// Replace <password>: the recorder does not see what you type into password fields.\n")
		}
	default:
		if device != "" {
			fmt.Fprintf(&b, "export DROIDLINE_DEVICE=%s\n", shellQuote(device))
		}
		if hasPassword {
			b.WriteString("# Replace <password>: the recorder does not see what you type into password fields.\n")
		}
	}
	for _, s := range steps {
		if line := s.Code(lang); line != "" {
			b.WriteString(line + "\n")
		}
	}
	if lang == "node" {
		b.WriteString("d.close();\n")
	}
	return b.String()
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

var plainShell = regexp.MustCompile(`^[A-Za-z0-9_./:@-]+$`)

func shellQuote(s string) string {
	if plainShell.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func pyLiteral(v any) string {
	switch t := v.(type) {
	case map[string]any:
		var parts []string
		for _, k := range sortedKeys(t) {
			parts = append(parts, strconv.Quote(k)+": "+pyLiteral(t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		var parts []string
		for _, x := range t {
			parts = append(parts, pyLiteral(x))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return strconv.Quote(t)
	case nil:
		return "None"
	}
	return fmt.Sprint(v)
}

var jsIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func jsLiteral(v any) string {
	switch t := v.(type) {
	case map[string]any:
		var parts []string
		for _, k := range sortedKeys(t) {
			key := k
			if !jsIdent.MatchString(k) {
				key = strconv.Quote(k)
			}
			parts = append(parts, key+": "+jsLiteral(t[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case []any:
		var parts []string
		for _, x := range t {
			parts = append(parts, jsLiteral(x))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return strconv.Quote(t)
	case nil:
		return "null"
	}
	return fmt.Sprint(v)
}
