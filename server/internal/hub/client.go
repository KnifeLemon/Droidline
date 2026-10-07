package hub

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/agentlink"
	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
	"github.com/KnifeLemon/Droidline/spec"
)

// Client is one SDK, CLI, MCP or HTTP connection.
type Client struct {
	hub       *Hub
	sendFn    func(map[string]any) error
	needsAuth bool

	mu        sync.Mutex
	authed    bool
	events    map[string]bool
	subDevice string
}

// NewClient registers a connection. needsAuth is true for non-loopback peers
// or when client.require_token is set.
func (h *Hub) NewClient(send func(map[string]any) error, needsAuth bool) *Client {
	c := &Client{hub: h, sendFn: send, needsAuth: needsAuth, events: map[string]bool{}}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (c *Client) Close() {
	c.hub.mu.Lock()
	delete(c.hub.clients, c)
	c.hub.mu.Unlock()
}

func (c *Client) send(m map[string]any) { c.sendFn(m) }

func (c *Client) wants(kind, device string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events[kind] && (c.subDevice == "" || c.subDevice == device)
}

// Exec runs one request and waits for its response (without id).
func (c *Client) Exec(req map[string]any) Result { return <-c.Start(req) }

// Start queues a request synchronously, so pipelined commands to one device keep
// their order, and returns a channel for the response.
func (c *Client) Start(req map[string]any) <-chan Result {
	p := c.prepare(req)
	if p.device != nil {
		return p.device
	}
	out := make(chan Result, 1)
	if p.now != nil {
		out <- p.now
	} else {
		go func() { out <- p.later() }()
	}
	return out
}

// prepared holds exactly one of: an immediate result, a device response
// channel, or server work to run off the reader goroutine.
type prepared struct {
	now    Result
	device <-chan Result
	later  func() Result
}

func (c *Client) prepare(req map[string]any) prepared {
	now := func(r Result) prepared { return prepared{now: r} }
	h := c.hub
	name, _ := req["cmd"].(string)
	if name == "" {
		return now(h.errResult("BAD_ARGS", map[string]any{"cmd": "?", "reason": "missing cmd"}))
	}
	if name == "auth" {
		tok, _ := req["token"].(string)
		if tok == "" || !h.store.CheckClientToken(tok) {
			return now(h.errResult("UNAUTHORIZED", nil))
		}
		c.mu.Lock()
		c.authed = true
		c.mu.Unlock()
		return now(Result{"ok": true})
	}
	c.mu.Lock()
	blocked := c.needsAuth && !c.authed
	c.mu.Unlock()
	if blocked {
		return now(h.errResult("UNAUTHORIZED", nil))
	}

	cmd, params, err := h.spec.Normalize(name, req)
	if err != nil {
		ae, _ := err.(*spec.ArgError)
		if ae != nil && ae.Unknown {
			r := h.errResult("UNKNOWN_CMD", map[string]any{"cmd": name, "agent": "server " + Version})
			if len(ae.Similar) > 0 {
				r["similar"] = ae.Similar
				r["msg"] = r["msg"].(string) + didYouMean(h.Lang(), ae.Similar)
			}
			return now(r)
		}
		reason := err.Error()
		if ae != nil {
			reason = ae.Reason
		}
		return now(h.errResult("BAD_ARGS", map[string]any{"cmd": name, "reason": reason}))
	}
	devRef, _ := req["device"].(string)

	switch cmd.Scope {
	case "server":
		return prepared{later: func() Result { return c.serverCmd(cmd, params, devRef) }}
	case "client":
		return now(h.errResult("BAD_ARGS", map[string]any{"cmd": name, "reason": "this is an SDK feature built on subscribe; it has no wire form"}))
	}

	d, errR := h.pickDevice(devRef)
	if errR != nil {
		return now(errR)
	}
	if cmd.Name == "proxy" {
		if r := h.resolveProxyProfile(params); r != nil {
			return now(r)
		}
	}
	wait, _ := req["wait"].(bool)
	offline := h.store.Config.OfflineWait
	if v, ok := req["offline_wait"].(float64); ok && v >= 0 {
		offline = v
	}
	call := &Call{Cmd: cmd, Params: params, Wait: wait, OfflineWait: time.Duration(offline * float64(time.Second))}
	reqID := req["id"]
	call.Late = func(r Result) {
		ev := map[string]any{"event": "result", "id": reqID, "device": d.ID}
		for k, v := range r {
			ev[k] = v
		}
		c.send(ev)
	}
	return prepared{device: d.Submit(call)}
}

func didYouMean(lang string, names []string) string {
	switch lang {
	case "ko":
		return ". 혹시: " + strings.Join(names, ", ")
	case "zh":
		return "。您是否想用：" + strings.Join(names, ", ")
	}
	return ". Did you mean: " + strings.Join(names, ", ")
}

func (h *Hub) resolveProxyProfile(params map[string]any) Result {
	u, _ := params["url"].(string)
	if !strings.HasPrefix(u, "@") {
		return nil
	}
	full := h.store.Secret("proxy:" + u[1:])
	if full == "" {
		return h.errResult("BAD_ARGS", map[string]any{"cmd": "proxy", "reason": fmt.Sprintf("no saved proxy %q; add it with droidline proxy add %s <url>", u[1:], u[1:])})
	}
	params["url"] = full
	return nil
}

func (c *Client) serverCmd(cmd *spec.Command, p map[string]any, devRef string) Result {
	h := c.hub
	switch cmd.Name {
	case "devices":
		list := []any{}
		for _, d := range h.sortedDevices() {
			list = append(list, d.Info())
		}
		return Result{"ok": true, "value": list, "pending": h.PendingPairs()}
	case "server_info":
		return Result{"ok": true, "version": Version, "server": h.serverID, "name": h.Name(), "proto": agentlink.Proto,
			"agent_port": h.ports[0], "client_port": h.ports[1], "lang": h.Lang(), "pub": h.PublicKey(),
			"tls_fp": h.tlsFP, "data_dir": h.store.Dir, "uptime_s": int(time.Since(h.started).Seconds())}
	case "pair":
		code := strings.TrimSpace(fmt.Sprint(p["code"]))
		name, _ := p["name"].(string)
		id, ok := h.approve(code, name)
		if !ok {
			return h.errResult("PAIRING_FAILED", map[string]any{"code": code})
		}
		// Registration finishes on the phone's goroutine; wait briefly for it.
		for i := 0; i < 50; i++ {
			if d := h.deviceByRef(id); d != nil && d.Online() {
				return Result{"ok": true, "value": d.Info()}
			}
			time.Sleep(100 * time.Millisecond)
		}
		return Result{"ok": true, "value": map[string]any{"id": id}}
	case "pair_qr":
		name, _ := p["name"].(string)
		uri, exp := h.NewEnrollment(name)
		return Result{"ok": true, "uri": uri, "expires": exp.Unix()}
	case "rename":
		d := h.deviceByRef(fmt.Sprint(p["device"]))
		if d == nil {
			return h.errResult("DEVICE_NOT_FOUND", map[string]any{"device": p["device"]})
		}
		name := strings.TrimSpace(fmt.Sprint(p["name"]))
		if name == "" || name == "_" || nameUnsafe.MatchString(name) {
			return h.errResult("BAD_ARGS", map[string]any{"cmd": "rename", "reason": "names may use letters, digits, dot, dash and underscore"})
		}
		if other := h.deviceByRef(name); other != nil && other != d {
			return h.errResult("BAD_ARGS", map[string]any{"cmd": "rename", "reason": fmt.Sprintf("%s is already used by %s", name, other.ID)})
		}
		d.mu.Lock()
		d.rec.Name = name
		rec := d.rec
		sess := d.sess
		d.mu.Unlock()
		if err := h.store.PutDevice(rec); err != nil {
			return h.errResult("INTERNAL", map[string]any{"reason": err.Error()})
		}
		// The app shows its own name; a connected phone hears about the change at once.
		if sess != nil {
			sess.Send(map[string]any{"event": "renamed", "name": name})
		}
		return Result{"ok": true, "value": d.Info()}
	case "revoke":
		d := h.deviceByRef(fmt.Sprint(p["device"]))
		if d == nil {
			return h.errResult("DEVICE_NOT_FOUND", map[string]any{"device": p["device"]})
		}
		if err := h.revoke(d); err != nil {
			return h.errResult("INTERNAL", map[string]any{"reason": err.Error()})
		}
		h.log.Info("revoked", "device", d.ID)
		return Result{"ok": true}
	case "subscribe":
		events, _ := p["events"].([]any)
		dev := ""
		if ref, _ := p["device"].(string); ref != "" {
			d := h.deviceByRef(ref)
			if d == nil {
				return h.errResult("DEVICE_NOT_FOUND", map[string]any{"device": ref})
			}
			dev = d.ID
		}
		c.mu.Lock()
		for _, e := range events {
			if s, ok := e.(string); ok {
				c.events[s] = true
			}
		}
		c.subDevice = dev
		c.mu.Unlock()
		return Result{"ok": true}
	case "wait_notification":
		d, errR := h.pickDevice(devRef)
		if errR != nil {
			return errR
		}
		return h.waitNotification(d, p)
	}
	return h.errResult("UNKNOWN_CMD", map[string]any{"cmd": cmd.Name, "agent": "server " + Version})
}

type notifWaiter struct {
	device string
	match  func(map[string]any) bool
	ch     chan map[string]any
}

func notifMatcher(p map[string]any) func(map[string]any) bool {
	by, _ := p["by"].(string)
	val, _ := p["value"].(string)
	pkg, _ := p["package"].(string)
	return func(n map[string]any) bool {
		title, _ := n["title"].(string)
		text, _ := n["text"].(string)
		npkg, _ := n["package"].(string)
		if pkg != "" && npkg != pkg {
			return false
		}
		switch by {
		case "text":
			return title == val || text == val
		case "textContains":
			return strings.Contains(title, val) || strings.Contains(text, val)
		case "title":
			return title == val
		case "package":
			return npkg == val
		}
		return false
	}
}

func (h *Hub) waitNotification(d *Device, p map[string]any) Result {
	w := &notifWaiter{device: d.ID, match: notifMatcher(p), ch: make(chan map[string]any, 1)}
	h.mu.Lock()
	h.waiters[w] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.waiters, w)
		h.mu.Unlock()
	}()
	timeout, _ := p["timeout"].(float64)
	select {
	case n := <-w.ch:
		v := copyMap(n)
		delete(v, "event")
		return Result{"ok": true, "value": v}
	case <-time.After(time.Duration(timeout * float64(time.Second))):
		return h.errResult("TIMEOUT", map[string]any{"cmd": "wait_notification", "timeout": timeout})
	}
}

func (h *Hub) onNotification(d *Device, n map[string]any) {
	h.broadcast("notification", d.ID, n)
	h.mu.Lock()
	for w := range h.waiters {
		if w.device == d.ID && w.match(n) {
			select {
			case w.ch <- n:
			default:
			}
		}
	}
	h.mu.Unlock()
	h.webhooks.deliver(d, n)
}

// configEvent tells a phone every route it can use to reach this PC.
func (h *Hub) configEvent(deviceID string) map[string]any {
	ev := map[string]any{"event": "config", "addresses": h.addresses()}
	if h.tlsFP != "" {
		ev["tls_fp"] = h.tlsFP
	}
	if u := h.store.Config.Relay.URL; u != "" {
		if tok := h.store.Secret("relay_token"); tok != "" {
			ev["relay"] = map[string]any{"url": u, "ticket": dlcrypto.DeviceTicket(tok, h.serverID, deviceID)}
		}
	}
	return ev
}

func (h *Hub) addresses() []string {
	out := append([]string{}, h.lanAddr()...)
	return append(out, h.store.Config.Remote.Addresses...)
}

func (h *Hub) pairingURI(tid string, token []byte, exp time.Time) string {
	q := url.Values{}
	q.Set("v", "1")
	q.Set("s", h.serverID)
	q.Set("n", h.Name())
	q.Set("k", h.PublicKey())
	q.Set("t", tid+"."+dlcrypto.B64(token))
	if h.tlsFP != "" {
		q.Set("f", h.tlsFP)
	}
	if u := h.store.Config.Relay.URL; u != "" {
		if tok := h.store.Secret("relay_token"); tok != "" {
			q.Set("r", u)
			q.Set("rt", dlcrypto.EnrollTicket(tok, h.serverID, exp.Unix()))
			q.Set("re", fmt.Sprint(exp.Unix()))
		}
	}
	// PROTOCOL section 5: each address is escaped on its own and joined with a
	// literal comma, so url.Values (which would escape the commas too) is not used for a.
	var addrs []string
	for _, a := range h.addresses() {
		addrs = append(addrs, url.QueryEscape(a))
	}
	return "droidline://pair?" + q.Encode() + "&a=" + strings.Join(addrs, ",")
}

// LANAddresses lists tcp:// URLs for each private IPv4 address of this machine.
func LANAddresses(port int) []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			out = append(out, fmt.Sprintf("tcp://%s:%d", ipn.IP, port))
		}
	}
	return out
}

// MarshalLine encodes a response or event as one NDJSON line.
func MarshalLine(m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"ok": false, "error": "INTERNAL", "msg": err.Error()})
	}
	return b
}
