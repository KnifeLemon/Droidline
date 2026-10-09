package inspect

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"

	"github.com/KnifeLemon/Droidline/server/internal/client"
	"github.com/KnifeLemon/Droidline/spec"
)

//go:embed web
var web embed.FS

type Inspector struct {
	cl       *client.Client
	loopback bool

	mu       sync.Mutex
	nextSnap int
	snaps    map[int]*snapshot
	records  map[string]*recording // by device ID
	watchers map[chan string]struct{}
}

type snapshot struct {
	device string
	nodes  []*spec.Node // pre-order, the index the page uses
	roots  []*spec.Node
}

type recording struct {
	On    bool   `json:"on"`
	Steps []Step `json:"steps"`
}

func New(cl *client.Client, loopback bool) *Inspector {
	return &Inspector{cl: cl, loopback: loopback, snaps: map[int]*snapshot{}, records: map[string]*recording{}, watchers: map[chan string]struct{}{}}
}

func (in *Inspector) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(web, "web")
	mux.Handle("GET /", http.FileServerFS(static))
	api := func(pattern string, fn func(r *http.Request, body map[string]any) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if r.Method == http.MethodPost {
				_ = json.NewDecoder(r.Body).Decode(&body)
			}
			v, err := fn(r, body)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(v)
		})
	}
	api("GET /api/devices", in.devices)
	api("POST /api/snapshot", in.snapshot)
	api("POST /api/suggest", in.suggest)
	api("POST /api/run", in.run)
	api("POST /api/record", in.record)
	api("POST /api/record/clear", in.clearRecord)
	api("GET /api/record", func(r *http.Request, _ map[string]any) (any, error) {
		return in.recordState(r.URL.Query().Get("device")), nil
	})
	mux.HandleFunc("GET /api/events", in.events)
	return in.guard(mux)
}

// guard lets only this page call the API: no other web page, and no other host name.
func (in *Inspector) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "other web pages may not use the inspector", http.StatusForbidden)
			return
		}
		if in.loopback {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}
			if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
				http.Error(w, "host not allowed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// call sends one command and turns ok:false into an error with the server's message.
func (in *Inspector) call(req map[string]any) (map[string]any, error) {
	res, err := in.cl.Call(req)
	if err != nil {
		return nil, err
	}
	if ok, _ := res["ok"].(bool); !ok {
		return res, fmt.Errorf("%v", res["msg"])
	}
	return res, nil
}

func (in *Inspector) devices(*http.Request, map[string]any) (any, error) {
	res, err := in.call(map[string]any{"cmd": "devices"})
	if err != nil {
		return nil, err
	}
	return res["value"], nil
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// snapshot takes the screen tree and, when the phone allows it, a screenshot. Every node
// gets "_i", its pre-order index, which the page sends back to ask for selectors.
func (in *Inspector) snapshot(_ *http.Request, body map[string]any) (any, error) {
	device := str(body, "device")
	if settle, _ := body["settle"].(bool); settle {
		// After a tap from the page, read the screen once it stops changing. A screen that
		// never settles is still read when the wait gives up.
		_, _ = in.call(map[string]any{"cmd": "wait_idle", "device": device, "ms": 500, "timeout": 5})
	}
	dump, err := in.call(map[string]any{"cmd": "dump", "device": device})
	if err != nil {
		return nil, err
	}
	tree, _ := dump["tree"].(map[string]any)
	nodes := numberTree(tree)
	snap := &snapshot{device: device, nodes: nodes, roots: nodes[:1]}

	out := map[string]any{"width": dump["width"], "height": dump["height"], "package": dump["package"], "activity": dump["activity"], "tree": tree}
	if shot, err := in.call(map[string]any{"cmd": "screenshot", "device": device, "format": "jpeg", "quality": 85}); err == nil {
		out["image"] = "data:image/jpeg;base64," + str(shot, "data")
	} else {
		out["image_error"] = err.Error()
	}
	in.mu.Lock()
	in.nextSnap++
	id := in.nextSnap
	in.snaps[id] = snap
	delete(in.snaps, id-20)
	in.mu.Unlock()
	out["snap"] = id
	return out, nil
}

// numberTree gives every node "_i", its pre-order index, and returns the linked nodes
// in that same order, so the index the page sends back finds the right node.
func numberTree(tree map[string]any) []*spec.Node {
	root := spec.NewTree(tree)
	var nodes []*spec.Node
	root.Walk(func(n *spec.Node) { nodes = append(nodes, n) })
	next := 0
	var number func(m map[string]any)
	number = func(m map[string]any) {
		m["_i"] = next
		next++
		kids, _ := m["children"].([]any)
		for _, k := range kids {
			if km, ok := k.(map[string]any); ok {
				number(km)
			}
		}
	}
	number(tree)
	return nodes
}

func guessCmd(n *spec.Node) string {
	switch {
	case n.Bool("editable"):
		return "input"
	case n.Bool("clickable"), n.Parent != nil && clickableAncestor(n):
		return "touch"
	}
	return "get_text"
}

func clickableAncestor(n *spec.Node) bool {
	for a := n.Parent; a != nil; a = a.Parent {
		if a.Bool("clickable") {
			return true
		}
	}
	return false
}

func (in *Inspector) suggest(_ *http.Request, body map[string]any) (any, error) {
	id, _ := body["snap"].(float64)
	idx, _ := body["index"].(float64)
	in.mu.Lock()
	snap := in.snaps[int(id)]
	in.mu.Unlock()
	if snap == nil || int(idx) < 0 || int(idx) >= len(snap.nodes) {
		return nil, fmt.Errorf("that screen is gone; refresh")
	}
	target := snap.nodes[int(idx)]
	cmd := str(body, "cmd")
	if cmd == "" {
		cmd = guessCmd(target)
	}
	var list []map[string]any
	for _, s := range Suggest(snap.roots, target) {
		sel := s
		step := Step{Cmd: cmd, Sel: &sel, Text: "text"}
		list = append(list, map[string]any{
			"selector": s,
			"code":     map[string]string{"python": step.Code("python"), "node": step.Code("node"), "cli": step.Code("cli")},
		})
	}
	return map[string]any{"cmd": cmd, "selectors": list}, nil
}

// run performs one step on the phone, so a selector can be tried from the page.
func (in *Inspector) run(_ *http.Request, body map[string]any) (any, error) {
	req := map[string]any{"cmd": str(body, "cmd"), "device": str(body, "device")}
	for _, k := range []string{"by", "value", "nth", "text", "x", "y"} {
		if v, ok := body[k]; ok && v != nil && v != "" {
			req[k] = v
		}
	}
	if _, isQuery := req["by"].(map[string]any); isQuery {
		delete(req, "value")
	}
	res, err := in.call(req)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (in *Inspector) deviceID(ref string) string {
	res, err := in.call(map[string]any{"cmd": "devices"})
	if err != nil {
		return ref
	}
	list, _ := res["value"].([]any)
	for _, d := range list {
		m, _ := d.(map[string]any)
		if m["id"] == ref || m["name"] == ref {
			return str(m, "id")
		}
	}
	if len(list) == 1 && ref == "" {
		m, _ := list[0].(map[string]any)
		return str(m, "id")
	}
	return ref
}

func (in *Inspector) record(_ *http.Request, body map[string]any) (any, error) {
	device := in.deviceID(str(body, "device"))
	on, _ := body["on"].(bool)
	if _, err := in.call(map[string]any{"cmd": "record", "device": device, "on": on}); err != nil {
		return nil, err
	}
	in.mu.Lock()
	rec := in.records[device]
	if rec == nil {
		rec = &recording{}
		in.records[device] = rec
	}
	rec.On = on
	in.mu.Unlock()
	if on && len(in.recordState(device).Steps) == 0 {
		if cur, err := in.call(map[string]any{"cmd": "current", "device": device}); err == nil && str(cur, "package") != "" {
			in.mu.Lock()
			rec.Steps = append(rec.Steps, Step{Cmd: "launch", Package: str(cur, "package")})
			in.mu.Unlock()
		}
	}
	in.notify(device)
	return in.recordState(device), nil
}

func (in *Inspector) clearRecord(_ *http.Request, body map[string]any) (any, error) {
	device := in.deviceID(str(body, "device"))
	in.mu.Lock()
	if rec := in.records[device]; rec != nil {
		rec.Steps = nil
	}
	in.mu.Unlock()
	in.notify(device)
	return in.recordState(device), nil
}

type recordView struct {
	Device  string              `json:"device"`
	On      bool                `json:"on"`
	Steps   []Step              `json:"steps"`
	Lines   []map[string]string `json:"lines"`
	Scripts map[string]string   `json:"scripts"`
}

func (in *Inspector) recordState(device string) recordView {
	device = in.deviceID(device)
	in.mu.Lock()
	rec := in.records[device]
	var steps []Step
	on := false
	if rec != nil {
		steps = append(steps, rec.Steps...)
		on = rec.On
	}
	in.mu.Unlock()
	v := recordView{Device: device, On: on, Steps: steps, Scripts: map[string]string{}, Lines: []map[string]string{}}
	for _, s := range steps {
		v.Lines = append(v.Lines, map[string]string{"python": s.Code("python"), "node": s.Code("node"), "cli": s.Code("cli")})
	}
	for _, lang := range []string{"python", "node", "cli"} {
		v.Scripts[lang] = Script(lang, "", steps)
	}
	return v
}

// Listen reads action events from the phones and turns them into steps.
func (in *Inspector) Listen(events <-chan map[string]any) {
	for ev := range events {
		if ev["event"] != "action" {
			continue
		}
		device := str(ev, "device")
		in.mu.Lock()
		rec := in.records[device]
		if rec == nil || !rec.On {
			in.mu.Unlock()
			continue
		}
		rec.Steps = applyAction(rec.Steps, ev)
		in.mu.Unlock()
		in.notify(device)
	}
}

// applyAction adds a recorded tap or edit. Typing into the field just tapped replaces
// that tap, and further typing updates the same input step.
func applyAction(steps []Step, ev map[string]any) []Step {
	target, _ := ev["target"].(map[string]any)
	var bounds [4]int
	if b, ok := target["bounds"].([]any); ok && len(b) == 4 {
		for i := range bounds {
			f, _ := b[i].(float64)
			bounds[i] = int(f)
		}
	}
	kind := str(ev, "kind")
	if kind == "input" && len(steps) > 0 {
		last := &steps[len(steps)-1]
		if last.Cmd == "input" && last.boundsEq(bounds) {
			last.Text, last.Password = str(ev, "text"), ev["password"] == true
			return steps
		}
	}
	screen, _ := ev["screen"].(map[string]any)
	tree, _ := screen["tree"].(map[string]any)
	if tree == nil {
		return steps
	}
	roots := []*spec.Node{spec.NewTree(tree)}
	var sel Selector
	if node := exact(roots, bounds, str(target, "class")); node != nil {
		sels := Suggest(roots, node)
		if len(sels) == 0 {
			return steps
		}
		sel = sels[0]
	} else if s, ok := fromTarget(target); ok {
		// The screen changed before it was read; name the element by its own fields.
		sel = s
	} else {
		return steps
	}
	step := Step{Cmd: map[string]string{"click": "touch", "long_click": "long_touch", "input": "input"}[kind], Sel: &sel, at: bounds}
	if kind == "input" {
		step.Text, step.Password = str(ev, "text"), ev["password"] == true
		if n := len(steps); n > 0 && steps[n-1].Cmd == "touch" && steps[n-1].boundsEq(bounds) {
			steps = steps[:n-1]
		}
	}
	return append(steps, step)
}

func (s Step) boundsEq(b [4]int) bool { return s.at == b }

func exact(roots []*spec.Node, bounds [4]int, class string) *spec.Node {
	var found *spec.Node
	for _, r := range roots {
		r.Walk(func(n *spec.Node) {
			if n.Bounds() == bounds && n.Str("class") == class {
				found = n
			}
		})
	}
	return found
}

// fromTarget names a tapped element from what the event says about it, without the
// screen to check that the name is unique.
func fromTarget(t map[string]any) (Selector, bool) {
	if id := str(t, "id"); id != "" {
		return Selector{By: "id", Value: id, Count: -1}, true
	}
	if text := str(t, "text"); text != "" {
		return Selector{By: "text", Value: text, Count: -1}, true
	}
	if desc := str(t, "desc"); desc != "" {
		return Selector{By: "desc", Value: desc, Count: -1}, true
	}
	if inner := str(t, "inner"); inner != "" {
		return Selector{By: map[string]any{"clickable": true, "has": map[string]any{"text": inner}}, Count: -1}, true
	}
	return Selector{}, false
}

func (in *Inspector) notify(device string) {
	b, _ := json.Marshal(in.recordState(device))
	msg := string(b)
	in.mu.Lock()
	defer in.mu.Unlock()
	for ch := range in.watchers {
		select {
		case ch <- msg:
		default:
		}
	}
}

// events streams recording updates to the page as server-sent events.
func (in *Inspector) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan string, 16)
	in.mu.Lock()
	in.watchers[ch] = struct{}{}
	in.mu.Unlock()
	defer func() {
		in.mu.Lock()
		delete(in.watchers, ch)
		in.mu.Unlock()
	}()
	fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()
	for {
		select {
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
