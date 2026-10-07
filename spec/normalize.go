package spec

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ArgError is reported to clients as BAD_ARGS (or UNKNOWN_CMD when Unknown is set).
type ArgError struct {
	Cmd     string
	Reason  string
	Unknown bool
	Similar []string
}

func (e *ArgError) Error() string {
	if e.Unknown {
		return "unknown command " + e.Cmd
	}
	return e.Cmd + ": " + e.Reason
}

// Envelope keys that belong to the request, not to the command.
var envelopeKeys = map[string]bool{
	"id": true, "cmd": true, "device": true, "wait": true, "offline_wait": true, "args": true,
}

// Normalize checks raw request fields against the command definition and returns
// the parameters the agent (or server handler) receives: aliases resolved,
// positional args mapped, values coerced, defaults filled, client-only params dropped.
func (s *Spec) Normalize(name string, raw map[string]any) (*Command, map[string]any, error) {
	c, aliasBy := s.Command(name)
	if c == nil {
		if bc := s.batch[name]; bc != nil {
			return nil, nil, &ArgError{Cmd: name, Reason: name + " can only be used as a batch step"}
		}
		return nil, nil, &ArgError{Cmd: name, Unknown: true, Similar: s.Similar(name, 3)}
	}
	out, err := s.normalizeFor(c, aliasBy, raw)
	return c, out, err
}

func (s *Spec) normalizeFor(c *Command, aliasBy string, raw map[string]any) (map[string]any, error) {
	bad := func(format string, a ...any) error {
		return &ArgError{Cmd: c.Name, Reason: fmt.Sprintf(format, a...)}
	}
	in := map[string]any{}
	for k, v := range raw {
		if !envelopeKeys[k] {
			in[k] = v
		}
	}

	if args, ok := raw["args"]; ok && args != nil {
		list, ok := args.([]any)
		if !ok {
			return nil, bad("args must be an array")
		}
		order := positional(c, aliasBy)
		if len(list) > len(order) {
			return nil, bad("takes at most %d positional arguments, got %d", len(order), len(list))
		}
		for i, v := range list {
			name := order[i].Name
			if _, dup := in[name]; dup {
				return nil, bad("%s given both by position and by name", name)
			}
			in[name] = v
		}
	}

	if aliasBy != "" {
		if v, ok := in["by"]; ok && v != aliasBy {
			return nil, bad("%s always matches by %s", c.Name, aliasBy)
		}
		in["by"] = aliasBy
	}

	out := map[string]any{}
	for k, v := range in {
		p := c.Param(k)
		if p == nil {
			return nil, bad("unknown parameter %q (accepted: %s)", k, strings.Join(paramNames(c), ", "))
		}
		if p.ClientOnly {
			continue
		}
		// SDKs may send null for an unset optional param; treat it as absent.
		if v == nil && !strings.Contains(p.Type, "null") {
			continue
		}
		cv, err := s.coerce(c, p, v)
		if err != nil {
			return nil, err
		}
		out[k] = cv
	}

	for _, p := range c.Params {
		if p.ClientOnly {
			continue
		}
		if _, ok := out[p.Name]; ok {
			continue
		}
		if p.Required {
			// swipe takes either a direction or four coordinates; checked below.
			return nil, bad("missing required parameter %q", p.Name)
		}
		if p.Default != nil {
			dv, err := s.coerce(c, &p, p.Default)
			if err != nil {
				return nil, err
			}
			out[p.Name] = dv
		}
	}

	if err := s.special(c, out); err != nil {
		return nil, err
	}
	return out, nil
}

func positional(c *Command, aliasBy string) []Param {
	var out []Param
	for _, p := range c.Params {
		if p.ClientOnly || (aliasBy != "" && p.Name == "by") {
			continue
		}
		out = append(out, p)
	}
	return out
}

func paramNames(c *Command) []string {
	var out []string
	for _, p := range c.Params {
		if !p.ClientOnly {
			out = append(out, p.Name)
		}
	}
	return out
}

func (s *Spec) coerce(c *Command, p *Param, v any) (any, error) {
	bad := func(format string, a ...any) error {
		return &ArgError{Cmd: c.Name, Reason: fmt.Sprintf("%s: ", p.Name) + fmt.Sprintf(format, a...)}
	}
	var out any
	switch p.Type {
	case "string":
		sv, ok := v.(string)
		if !ok {
			return nil, bad("expected a string")
		}
		out = sv
	case "int":
		n, ok := asInt(v)
		if !ok {
			return nil, bad("expected an integer")
		}
		out = n
	case "number":
		f, ok := asFloat(v)
		if !ok {
			return nil, bad("expected a number")
		}
		out = f
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, bad("expected true or false")
		}
		out = b
	case "selector":
		sv, ok := v.(string)
		if !ok || !s.IsSelector(sv) {
			return nil, bad("expected one of %s", strings.Join(s.SelectorNames(), ", "))
		}
		out = sv
	case "int|string":
		if n, ok := asInt(v); ok {
			out = n
		} else if sv, ok := v.(string); ok {
			out = sv
		} else {
			return nil, bad("expected an integer or a string")
		}
	case "string|null":
		if v == nil {
			out = nil
		} else if sv, ok := v.(string); ok {
			out = sv
		} else {
			return nil, bad("expected a string or null")
		}
	case "string|list<string>", "list<string>":
		list, err := stringList(v, p.Type == "string|list<string>")
		if err != nil {
			return nil, bad("%v", err)
		}
		out = list
	case "list<selector_pair>":
		pairs, err := s.selectorPairs(v)
		if err != nil {
			return nil, bad("%v", err)
		}
		out = pairs
	case "list<step>":
		steps, err := s.batchSteps(v)
		if err != nil {
			return nil, &ArgError{Cmd: c.Name, Reason: err.Error()}
		}
		out = steps
	default:
		out = v
	}
	if len(p.Enum) > 0 {
		sv, _ := out.(string)
		if !contains(p.Enum, sv) {
			return nil, bad("expected one of %s", strings.Join(p.Enum, ", "))
		}
	}
	return out, nil
}

// special holds the few cross-parameter rules commands.json cannot express.
func (s *Spec) special(c *Command, p map[string]any) error {
	bad := func(format string, a ...any) error {
		return &ArgError{Cmd: c.Name, Reason: fmt.Sprintf(format, a...)}
	}
	switch c.Name {
	case "swipe":
		if dir, ok := p["x1"].(string); ok {
			if !contains([]string{"up", "down", "left", "right"}, dir) {
				return bad("direction must be up, down, left or right")
			}
			for _, k := range []string{"y1", "x2", "y2"} {
				if _, ok := p[k]; ok {
					return bad("pass either a direction or four coordinates")
				}
			}
			return nil
		}
		for _, k := range []string{"y1", "x2", "y2"} {
			if _, ok := p[k]; !ok {
				return bad("coordinate swipe needs x1, y1, x2, y2")
			}
		}
	case "sendkey":
		_, hasKey := p["key"]
		_, hasText := p["text"]
		if !hasKey && !hasText {
			return bad("pass a key name, a key code, or text")
		}
	case "screenshot":
		if q, ok := p["quality"].(int64); ok && (q < 1 || q > 100) {
			return bad("quality must be 1-100")
		}
		if sc, ok := p["scale"].(float64); ok && (sc < 0.1 || sc > 1) {
			return bad("scale must be 0.1-1.0")
		}
	case "proxy":
		if u, ok := p["url"].(string); ok {
			if u == "off" || u == "" {
				p["url"] = nil
			} else if !strings.HasPrefix(u, "@") {
				if err := checkProxyURL(u); err != nil {
					return bad("%v", err)
				}
			}
		}
		if p["url"] != nil {
			if _, ok := p["app"]; !ok {
				return bad("app is required when turning the proxy on")
			}
		}
	}
	for _, k := range []string{"x", "y", "x1", "y1", "x2", "y2"} {
		if n, ok := p[k].(int64); ok && n < 0 {
			return bad("%s must not be negative", k)
		}
	}
	for _, k := range []string{"timeout", "max_age"} {
		if f, ok := p[k].(float64); ok && (f < 0 || f > 3600) {
			return bad("%s must be between 0 and 3600 seconds", k)
		}
	}
	return nil
}

func checkProxyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("proxy url must look like socks5://user:pass@host:port or http://host:port")
	}
	if u.Scheme != "socks5" && u.Scheme != "http" {
		return fmt.Errorf("proxy scheme must be socks5 or http, got %q", u.Scheme)
	}
	if u.Port() == "" {
		return fmt.Errorf("proxy url needs a port")
	}
	return nil
}

func (s *Spec) selectorPairs(v any) ([]any, error) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("expected a non-empty list of [by, value] pairs")
	}
	out := make([]any, 0, len(list))
	for i, item := range list {
		var by, val any
		switch t := item.(type) {
		case []any:
			if len(t) != 2 {
				return nil, fmt.Errorf("candidate %d must be [by, value]", i)
			}
			by, val = t[0], t[1]
		case map[string]any:
			by, val = t["by"], t["value"]
		default:
			return nil, fmt.Errorf("candidate %d must be [by, value]", i)
		}
		bs, _ := by.(string)
		vs, ok := val.(string)
		if !s.IsSelector(bs) || !ok {
			return nil, fmt.Errorf("candidate %d: by must be one of %s and value a string", i, strings.Join(s.SelectorNames(), ", "))
		}
		out = append(out, []any{bs, vs})
	}
	return out, nil
}

func (s *Spec) batchSteps(v any) ([]any, error) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("steps must be a non-empty list")
	}
	out := make([]any, 0, len(list))
	for i, item := range list {
		var name string
		raw := map[string]any{}
		switch t := item.(type) {
		case []any:
			if len(t) == 0 {
				return nil, fmt.Errorf("step %d is empty", i)
			}
			name, _ = t[0].(string)
			raw["args"] = append([]any{}, t[1:]...)
		case map[string]any:
			name, _ = t["cmd"].(string)
			for k, v := range t {
				if k != "cmd" {
					raw[k] = v
				}
			}
		default:
			return nil, fmt.Errorf("step %d must be [cmd, args...] or {\"cmd\": ...}", i)
		}
		if name == "" {
			return nil, fmt.Errorf("step %d has no command", i)
		}
		var c *Command
		aliasBy := ""
		if bc := s.batch[name]; bc != nil {
			c = bc
		} else {
			c, aliasBy = s.Command(name)
			if c == nil {
				return nil, fmt.Errorf("step %d: unknown command %q", i, name)
			}
			if c.Scope != "device" || c.Name == "batch" {
				return nil, fmt.Errorf("step %d: %s cannot run inside a batch", i, c.Name)
			}
		}
		params, err := s.normalizeFor(c, aliasBy, raw)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %v", i, name, err.(*ArgError).Reason)
		}
		params["cmd"] = c.Name
		out = append(out, params)
	}
	return out, nil
}

// CutsNetwork reports whether this normalised call will drop the phone's link.
func (s *Spec) CutsNetwork(c *Command, params map[string]any) bool {
	if c.Name == "batch" {
		steps, _ := params["steps"].([]any)
		for _, st := range steps {
			m, _ := st.(map[string]any)
			name, _ := m["cmd"].(string)
			if sc := s.byName[name]; sc != nil && s.CutsNetwork(sc, m) {
				return true
			}
		}
		return false
	}
	if c.CutsNetwork == nil {
		return false
	}
	v, _ := params[c.CutsNetwork.Param].(bool)
	return v == c.CutsNetwork.When
}

func stringList(v any, allowSingle bool) ([]any, error) {
	if sv, ok := v.(string); ok && allowSingle {
		return []any{sv}, nil
	}
	list, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			out := make([]any, len(ss))
			for i := range ss {
				out[i] = ss[i]
			}
			return out, nil
		}
		return nil, fmt.Errorf("expected a list of strings")
	}
	for _, it := range list {
		if _, ok := it.(string); !ok {
			return nil, fmt.Errorf("expected a list of strings")
		}
	}
	return list, nil
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == math.Trunc(n) && !math.IsInf(n, 0) {
			return int64(n), true
		}
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ParseArg converts one command-line string into the JSON value a param expects.
func (s *Spec) ParseArg(p *Param, raw string) (any, error) {
	switch p.Type {
	case "int":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer", p.Name)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number", p.Name)
		}
		return f, nil
	case "bool":
		switch strings.ToLower(raw) {
		case "true", "on", "1", "yes":
			return true, nil
		case "false", "off", "0", "no":
			return false, nil
		}
		return nil, fmt.Errorf("%s must be true/false (on/off also work)", p.Name)
	case "int|string":
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n, nil
		}
		return raw, nil
	case "string|null":
		if raw == "off" || raw == "null" {
			return nil, nil
		}
		return raw, nil
	case "string|list<string>", "list<string>":
		if strings.HasPrefix(raw, "[") {
			return parseJSONArg(raw)
		}
		var out []any
		for _, part := range strings.Split(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out, nil
	case "list<selector_pair>", "list<step>", "object", "list<object>":
		return parseJSONArg(raw)
	}
	return raw, nil
}

// SortedErrorCodes is used by generators and docs that want a stable order.
func (s *Spec) SortedErrorCodes() []string {
	out := make([]string, 0, len(s.Errors))
	for _, e := range s.Errors {
		out = append(out, e.Code)
	}
	sort.Strings(out)
	return out
}
