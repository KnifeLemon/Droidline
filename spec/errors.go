package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)

// RenderError fills the message template of code in lang. Fields the template
// needs but the caller did not supply render as "?".
func (s *Spec) RenderError(code, lang string, fields map[string]any) string {
	def := s.Error(code)
	if def == nil {
		if m, ok := fields["msg"].(string); ok && m != "" {
			return m
		}
		return code
	}
	if _, ok := fields["target"]; !ok {
		if by, ok := fields["by"].(string); ok {
			fields["target"] = fmt.Sprintf("%s '%v'", by, fields["value"])
		}
	}
	return placeholder.ReplaceAllStringFunc(def.Msg.In(lang), func(m string) string {
		v, ok := fields[m[1:len(m)-1]]
		if !ok || v == nil {
			return "?"
		}
		return formatValue(v)
	})
}

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = formatValue(x)
		}
		return strings.Join(parts, ", ")
	case []string:
		return strings.Join(t, ", ")
	}
	return fmt.Sprint(v)
}

func parseJSONArg(raw string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("expected JSON, for example [[\"text\",\"OK\"]]: %v", err)
	}
	return v, nil
}
