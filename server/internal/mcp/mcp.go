// Package mcp exposes every Droidline command as an MCP tool over stdio.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/KnifeLemon/Droidline/server/internal/client"
	"github.com/KnifeLemon/Droidline/server/internal/hub"
	"github.com/KnifeLemon/Droidline/server/internal/server"
	"github.com/KnifeLemon/Droidline/server/internal/store"
	"github.com/KnifeLemon/Droidline/spec"
)

type Options struct {
	Addr, Token, Device, Home string
}

var versions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const instructions = `Droidline controls Android phones. Work like this:
1. Call dump to see the current screen as a tree. Each element has text, id and desc.
2. Act on elements with touch, input, long_touch or scroll_to, passing by ("text", "id", "desc", "textContains", "descContains", "class") and the value from the dump. These wait up to 10 s for the element and fall back to a coordinate tap by themselves, so do not add waits.
3. Use tap(x, y) only for screens without elements, such as games.
4. Branch with exists, which, checked, get_text, in_app: they answer at once and never fail when the element is missing.
5. If more than one phone is online, pass device (an ID or name from devices).
Commands that turn off data, Wi-Fi or turn on airplane mode return accepted first; pass wait=true to get the final result.`

type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Run serves MCP on stdin/stdout until stdin closes. If no server is running it
// starts one inside this process, logging to stderr so stdout stays protocol-only.
func Run(o Options) error {
	cl, err := client.Dial(o.Addr, o.Token)
	if errors.Is(err, client.ErrNotRunning) {
		st, serr := store.Open(o.Home)
		if serr != nil {
			return serr
		}
		srv, serr := server.Start(st, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		if serr != nil {
			return fmt.Errorf("no server was running and starting one failed: %w", serr)
		}
		defer srv.Close()
		fmt.Fprintln(os.Stderr, "droidline mcp: started an embedded server")
		cl, err = client.Dial(fmt.Sprintf("127.0.0.1:%d", srv.ClientPort), o.Token)
	}
	if err != nil {
		return err
	}
	defer cl.Close()
	s := &session{cl: cl, sp: spec.MustLoad(), device: o.Device, out: bufio.NewWriter(os.Stdout)}
	return s.serve(os.Stdin)
}

type session struct {
	cl     *client.Client
	sp     *spec.Spec
	device string
	wmu    sync.Mutex
	out    *bufio.Writer
}

func (s *session) serve(in io.Reader) error {
	r := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var req rpc
			if jerr := json.Unmarshal(line, &req); jerr != nil {
				s.reply(nil, nil, &rpcErr{-32700, "parse error"})
			} else if req.ID == nil {
				// Notifications such as notifications/initialized need no answer.
			} else {
				go s.handle(req)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *session) reply(id json.RawMessage, result any, e *rpcErr) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	b, _ := json.Marshal(msg)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.out.Write(append(b, '\n'))
	s.out.Flush()
}

func (s *session) handle(req rpc) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := versions[0]
		for _, known := range versions {
			if known == p.ProtocolVersion {
				v = known
			}
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			"serverInfo":      map[string]any{"name": "droidline", "version": hub.Version},
			"instructions":    instructions,
		}, nil)
	case "ping":
		s.reply(req.ID, map[string]any{}, nil)
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": s.tools()}, nil)
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.reply(req.ID, nil, &rpcErr{-32602, "bad params"})
			return
		}
		s.reply(req.ID, s.callTool(p.Name, p.Arguments), nil)
	case "resources/list":
		s.reply(req.ID, map[string]any{"resources": []any{
			map[string]any{"uri": "droidline://devices", "name": "devices", "description": "Paired phones", "mimeType": "application/json"},
		}}, nil)
	case "resources/templates/list":
		s.reply(req.ID, map[string]any{"resourceTemplates": []any{
			map[string]any{"uriTemplate": "droidline://devices/{device}/screen", "name": "screen",
				"description": "Current screen tree of a phone (same as the dump tool)", "mimeType": "application/json"},
		}}, nil)
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		json.Unmarshal(req.Params, &p)
		s.readResource(req.ID, p.URI)
	default:
		s.reply(req.ID, nil, &rpcErr{-32601, "method not found: " + req.Method})
	}
}

func toolName(cmd string) string { return strings.ReplaceAll(cmd, ".", "_") }

func (s *session) tools() []any {
	var out []any
	for _, c := range s.sp.Commands {
		if !c.InMCP() {
			continue
		}
		props := map[string]any{}
		var required []string
		for _, p := range c.Params {
			if p.ClientOnly {
				continue
			}
			props[p.Name] = s.schema(p)
			if p.Required {
				required = append(required, p.Name)
			}
		}
		if c.Scope == "device" || c.Name == "wait_notification" {
			props["device"] = map[string]any{"type": "string", "description": "Phone ID or name. Optional when exactly one phone is online."}
		}
		if c.CutsNetwork != nil || c.Name == "batch" {
			props["wait"] = map[string]any{"type": "boolean", "description": "Wait for the final result after the phone reconnects instead of returning accepted."}
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		desc := c.Summary.In("en")
		if n := c.Notes.In("en"); n != "" {
			desc += ". " + n
		}
		if c.Example != "" {
			desc += " Example: " + c.Example
		}
		tool := map[string]any{"name": toolName(c.Name), "description": desc, "inputSchema": schema}
		readOnly := c.Condition || c.Group == "check" || c.Group == "screen" || c.Name == "devices" || c.Name == "info"
		destructive := c.Macro || c.Name == "proxy" || c.Name == "batch"
		tool["annotations"] = map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive, "openWorldHint": false}
		out = append(out, tool)
	}
	return out
}

func (s *session) schema(p spec.Param) map[string]any {
	desc := p.Doc.In("en")
	if p.Default != nil {
		desc += fmt.Sprintf(" Default %v.", p.Default)
	}
	m := map[string]any{"description": desc}
	switch p.Type {
	case "string":
		m["type"] = "string"
	case "int":
		m["type"] = "integer"
	case "number":
		m["type"] = "number"
	case "bool":
		m["type"] = "boolean"
	case "selector":
		m["type"] = "string"
		m["enum"] = s.sp.SelectorNames()
	case "int|string":
		m["type"] = []string{"integer", "string"}
	case "string|null":
		m["type"] = []string{"string", "null"}
	case "string|list<string>":
		m["anyOf"] = []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}
	case "list<string>":
		m["type"] = "array"
		m["items"] = map[string]any{"type": "string"}
	case "list<selector_pair>":
		m["type"] = "array"
		m["items"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 2, "maxItems": 2}
	case "list<step>":
		m["type"] = "array"
		m["items"] = map[string]any{"anyOf": []any{map[string]any{"type": "array"}, map[string]any{"type": "object"}}}
	}
	if len(p.Enum) > 0 {
		m["enum"] = p.Enum
	}
	return m
}

func (s *session) callTool(name string, args map[string]any) map[string]any {
	cmdName := name
	for _, c := range s.sp.Commands {
		if toolName(c.Name) == name {
			cmdName = c.Name
		}
	}
	req := map[string]any{"cmd": cmdName}
	for k, v := range args {
		req[k] = v
	}
	if _, set := req["device"]; !set && s.device != "" {
		req["device"] = s.device
	}
	res, err := s.cl.Call(req)
	if err != nil {
		return textResult(err.Error(), true)
	}
	if ok, _ := res["ok"].(bool); !ok {
		return textResult(fmt.Sprintf("%v: %v", res["error"], res["msg"]), true)
	}
	delete(res, "ok")
	switch cmdName {
	case "screenshot":
		mime := "image/jpeg"
		if res["format"] == "png" {
			mime = "image/png"
		}
		return map[string]any{"content": []any{
			map[string]any{"type": "image", "data": res["data"], "mimeType": mime},
			map[string]any{"type": "text", "text": fmt.Sprintf("%vx%v", res["width"], res["height"])},
		}}
	case "dump":
		if t, ok := res["tree"].(map[string]any); ok {
			res["tree"] = compact(t)
		}
	}
	if v, only := res["value"]; only && len(res) == 1 {
		b, _ := json.Marshal(v)
		return textResult(string(b), false)
	}
	b, _ := json.Marshal(res)
	return textResult(string(b), false)
}

func textResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}
}

// compact drops empty and false fields so a dump costs the model fewer tokens.
func compact(n map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range n {
		switch t := v.(type) {
		case string:
			if t != "" {
				out[k] = t
			}
		case bool:
			if t && k != "enabled" {
				out[k] = true
			}
			if !t && k == "enabled" {
				out["disabled"] = true
			}
		case []any:
			if k == "children" {
				var kids []any
				for _, c := range t {
					if cm, ok := c.(map[string]any); ok {
						kids = append(kids, compact(cm))
					}
				}
				if len(kids) > 0 {
					out[k] = kids
				}
			} else {
				out[k] = t
			}
		default:
			out[k] = v
		}
	}
	delete(out, "package")
	return out
}

func (s *session) readResource(id json.RawMessage, uri string) {
	var req map[string]any
	switch {
	case uri == "droidline://devices":
		req = map[string]any{"cmd": "devices"}
	case strings.HasPrefix(uri, "droidline://devices/") && strings.HasSuffix(uri, "/screen"):
		dev := strings.TrimSuffix(strings.TrimPrefix(uri, "droidline://devices/"), "/screen")
		req = map[string]any{"cmd": "dump", "device": dev}
	default:
		s.reply(id, nil, &rpcErr{-32002, "resource not found: " + uri})
		return
	}
	res, err := s.cl.Call(req)
	if err != nil {
		s.reply(id, nil, &rpcErr{-32603, err.Error()})
		return
	}
	if t, ok := res["tree"].(map[string]any); ok {
		res["tree"] = compact(t)
	}
	b, _ := json.Marshal(res)
	s.reply(id, map[string]any{"contents": []any{map[string]any{"uri": uri, "mimeType": "application/json", "text": string(b)}}}, nil)
}
