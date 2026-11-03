package hub

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/KnifeLemon/Droidline/spec"
)

// waitSMS polls the phone's inbox until a matching message arrives after the call started.
// SMS and MMS number their ids separately, so a message is new when its (mms, id) was not in the inbox at the start
// and it is no older than the newest message seen then.
func (h *Hub) waitSMS(d *Device, cmd *spec.Command, p map[string]any) Result {
	timeout, _ := p["timeout"].(float64)
	start := time.Now()
	deadline := start.Add(time.Duration(timeout * float64(time.Second)))

	first := h.deviceCall(d, "sms", map[string]any{"box": "inbox", "max": 50})
	if failed(first) {
		return first
	}
	seen := map[string]bool{}
	var newest float64
	for _, it := range list(first["value"]) {
		seen[smsKey(it)] = true
		if t := number(it["time"]); t > newest {
			newest = t
		}
	}

	query := map[string]any{"box": "inbox", "max": 20}
	for _, k := range []string{"address", "contains"} {
		if v, ok := p[k].(string); ok && v != "" {
			query[k] = v
		}
	}
	for {
		// Only messages since the call began can be new; the margin covers a phone clock that runs behind the PC.
		query["within"] = time.Since(start).Seconds() + 300
		r := h.deviceCall(d, "sms", query)
		if failed(r) {
			return r
		}
		for _, it := range list(r["value"]) {
			if !seen[smsKey(it)] && number(it["time"]) >= newest {
				return Result{"ok": true, "value": it}
			}
		}
		if !time.Now().Before(deadline) {
			return h.errResult("TIMEOUT", map[string]any{"cmd": cmd.Name, "timeout": timeout})
		}
		time.Sleep(time.Second)
	}
}

func list(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// number reads a JSON number from a phone reply, which keeps numbers as json.Number.
func number(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	}
	return 0
}

func smsKey(m map[string]any) string { return fmt.Sprintf("%v/%v", m["mms"], m["id"]) }
