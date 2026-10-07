package hub

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/agentlink"
	"github.com/KnifeLemon/Droidline/server/internal/store"
	"github.com/KnifeLemon/Droidline/spec"
)

// Call is one command on its way to a phone.
type Call struct {
	Cmd         *spec.Command
	Params      map[string]any
	Wait        bool
	OfflineWait time.Duration
	// Late receives the final result of a command the phone answered with accepted.
	Late func(Result)

	id           uint64
	done         chan Result
	once         sync.Once
	answered     chan struct{}
	answeredOnce sync.Once
}

func (c *Call) finish(r Result) {
	c.once.Do(func() { c.done <- r })
}

type Device struct {
	ID  string
	hub *Hub

	mu        sync.Mutex
	rec       store.DeviceRecord
	sess      *agentlink.Session
	hello     agentlink.Hello
	onlineSig chan struct{}
	nextID    uint64
	inflight  *Call
	accepted  map[uint64]*Call
	boot      string
	lastN     uint64
	ackedN    uint64

	queue chan *Call
	gone  chan struct{}
	once  sync.Once
}

func newDevice(h *Hub, rec store.DeviceRecord) *Device {
	d := &Device{
		ID: rec.ID, hub: h, rec: rec, onlineSig: make(chan struct{}),
		accepted: map[uint64]*Call{}, queue: make(chan *Call, 256), gone: make(chan struct{}),
	}
	go d.run()
	return d
}

func (d *Device) Name() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rec.Name
}

func (d *Device) pairedAt() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rec.PairedAt
}

func (d *Device) setRecord(r store.DeviceRecord) {
	d.mu.Lock()
	d.rec = r
	d.mu.Unlock()
}

func (d *Device) Online() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sess != nil
}

func (d *Device) Info() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	info := map[string]any{
		"id": d.ID, "name": d.rec.Name, "model": d.rec.Model, "sdk": d.rec.SDK,
		"release": d.rec.Release, "agent": d.rec.Agent, "online": d.sess != nil,
		"last_seen": d.rec.LastSeen,
	}
	if d.sess != nil {
		info["route"] = d.hello.Route
		info["ready"] = d.hello.Ready
		info["last_seen"] = time.Now().Unix()
	}
	return info
}

// Submit queues a call in arrival order and returns where its response will land.
func (d *Device) Submit(c *Call) <-chan Result {
	c.done = make(chan Result, 1)
	c.answered = make(chan struct{})
	select {
	case d.queue <- c:
	case <-d.gone:
		c.done <- d.hub.errResult("DEVICE_NOT_FOUND", map[string]any{"device": d.ID})
	}
	return c.done
}

func (d *Device) run() {
	for {
		select {
		case c := <-d.queue:
			d.exec(c)
		case <-d.gone:
			for {
				select {
				case c := <-d.queue:
					c.finish(d.hub.errResult("DEVICE_NOT_FOUND", map[string]any{"device": d.ID}))
				default:
					return
				}
			}
		}
	}
}

func (d *Device) waitOnline(timeout time.Duration) bool {
	d.mu.Lock()
	if d.sess != nil {
		d.mu.Unlock()
		return true
	}
	sig := d.onlineSig
	d.mu.Unlock()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-sig:
		return true
	case <-t.C:
		return false
	case <-d.gone:
		return false
	}
}

func (d *Device) exec(c *Call) {
	if !d.waitOnline(c.OfflineWait) {
		c.finish(d.hub.errResult("DEVICE_OFFLINE", map[string]any{"device": d.Name(), "timeout": c.OfflineWait.Seconds()}))
		return
	}
	d.mu.Lock()
	d.nextID++
	c.id = d.nextID
	d.inflight = c
	sess := d.sess
	d.mu.Unlock()

	// A failed send is fine: the call stays in flight and is resent on reconnect.
	sess.Send(wireMessage(c))

	timer := time.NewTimer(callTimeout(c))
	defer timer.Stop()
	select {
	case <-c.doneSignal():
	case <-timer.C:
		d.mu.Lock()
		if d.inflight == c {
			d.inflight = nil
		}
		d.mu.Unlock()
		c.finish(d.hub.errResult("TIMEOUT", map[string]any{"cmd": c.Cmd.Name, "timeout": callTimeout(c).Seconds()}))
	case <-d.gone:
		c.finish(d.hub.errResult("DEVICE_NOT_FOUND", map[string]any{"device": d.ID}))
	}
}

// doneSignal fires once the call has its first answer (result or accepted).
func (c *Call) doneSignal() <-chan struct{} { return c.answered }

func wireMessage(c *Call) map[string]any {
	m := make(map[string]any, len(c.Params)+2)
	for k, v := range c.Params {
		m[k] = v
	}
	m["id"] = c.id
	m["cmd"] = c.Cmd.Name
	return m
}

// callTimeout covers the command's own wait plus the time a settings macro or
// a slow mobile link may add, and an offline gap within the call.
func callTimeout(c *Call) time.Duration {
	t := 30 * time.Second
	if f, ok := c.Params["timeout"].(float64); ok {
		t += time.Duration(f * float64(time.Second))
	}
	if c.Cmd.Name == "batch" {
		t += 5 * time.Minute
	}
	return t + c.OfflineWait
}

func (d *Device) attach(sess *agentlink.Session, hello agentlink.Hello, in <-chan inbound) {
	d.mu.Lock()
	old := d.sess
	sameBoot := hello.Boot != "" && hello.Boot == d.boot
	var restarted []*Call
	if !sameBoot {
		if d.inflight != nil {
			restarted = append(restarted, d.inflight)
			d.inflight = nil
		}
		for id, c := range d.accepted {
			restarted = append(restarted, c)
			delete(d.accepted, id)
		}
		d.boot = hello.Boot
		d.lastN, d.ackedN = 0, 0
	}
	ack := d.lastN
	d.sess = sess
	d.hello = hello
	d.rec.Model, d.rec.Manufacturer, d.rec.SDK = hello.Model, hello.Manufacturer, hello.SDK
	d.rec.Release, d.rec.Agent = hello.Release, hello.Agent
	rec := d.rec
	resend := d.inflight
	sig := d.onlineSig
	d.mu.Unlock()

	if old != nil && old != sess {
		old.Close()
	}
	for _, c := range restarted {
		r := d.hub.errResult("AGENT_RESTARTED", map[string]any{"device": rec.Name, "cmd": c.Cmd.Name})
		c.answer(r)
	}
	d.hub.store.PutDevice(rec)

	sess.Send(map[string]any{
		"event": "welcome", "server": d.hub.serverID, "name": d.hub.Name(), "lang": d.hub.Lang(),
		"ack": ack, "ping": int(pingInterval / time.Second),
	})
	sess.Send(d.hub.configEvent(d.ID))
	if resend != nil {
		sess.Send(wireMessage(resend))
	}
	// sig is closed exactly while a session is attached. A phone back from a
	// dead mobile link replaces a session the server still thinks is alive.
	if old == nil {
		close(sig)
	}

	d.hub.log.Info("device online", "device", rec.Name, "route", hello.Route, "via", sess.Transport(), "remote", sess.RemoteAddr())
	d.hub.broadcast("device", d.ID, map[string]any{"event": "device", "device": d.ID, "name": rec.Name, "state": "online", "route": hello.Route})
	go d.readLoop(sess, in)
}

func (d *Device) readLoop(sess *agentlink.Session, in <-chan inbound) {
	defer d.hub.recoverConn("device "+d.ID, sess.RemoteAddr())
	ackTick := time.NewTicker(5 * time.Second)
	defer ackTick.Stop()
	silence := time.NewTimer(3 * pingInterval)
	defer silence.Stop()
	defer d.detach(sess)
	for {
		select {
		case msg, ok := <-in:
			if !ok || msg.err != nil {
				return
			}
			silence.Reset(3 * pingInterval)
			d.handle(sess, msg.m)
		case <-ackTick.C:
			d.mu.Lock()
			n, acked := d.lastN, d.ackedN
			d.ackedN = n
			d.mu.Unlock()
			if n > acked {
				sess.Send(map[string]any{"event": "ack", "n": n})
			}
		case <-silence.C:
			d.hub.log.Info("device silent, dropping link", "device", d.Name())
			return
		case <-d.gone:
			return
		}
	}
}

func (d *Device) detach(sess *agentlink.Session) {
	sess.Close()
	d.mu.Lock()
	if d.sess != sess {
		d.mu.Unlock()
		return
	}
	d.sess = nil
	d.onlineSig = make(chan struct{})
	name := d.rec.Name
	d.mu.Unlock()
	d.hub.store.TouchDevice(d.ID, time.Now())
	d.hub.log.Info("device offline", "device", name)
	d.hub.broadcast("device", d.ID, map[string]any{"event": "device", "device": d.ID, "name": name, "state": "offline"})
}

func (d *Device) handle(sess *agentlink.Session, m map[string]any) {
	if n := agentlink.Uint(m, "n"); n > 0 {
		d.mu.Lock()
		dup := n <= d.lastN
		if !dup {
			d.lastN = n
		}
		d.mu.Unlock()
		if dup {
			return
		}
		delete(m, "n")
	}
	if ev := agentlink.Str(m, "event"); ev != "" {
		d.onEvent(sess, ev, m)
		return
	}
	if id := agentlink.Uint(m, "id"); id > 0 {
		d.onResponse(id, m)
	}
}

func (d *Device) onResponse(id uint64, m map[string]any) {
	d.mu.Lock()
	c := d.inflight
	if c == nil || c.id != id {
		d.mu.Unlock()
		return
	}
	d.inflight = nil
	accepted, _ := m["accepted"].(bool)
	if accepted {
		d.accepted[id] = c
	}
	d.mu.Unlock()
	delete(m, "id")
	r := d.hub.finishResult(d, c, Result(m))
	if accepted {
		if c.Wait {
			c.markAnswered()
			go d.expireAccepted(c)
			return
		}
		c.answer(r)
		return
	}
	c.answer(r)
}

// expireAccepted gives up on a waited-for result after the offline window.
func (d *Device) expireAccepted(c *Call) {
	time.Sleep(c.OfflineWait + 2*time.Minute)
	d.mu.Lock()
	_, still := d.accepted[c.id]
	delete(d.accepted, c.id)
	d.mu.Unlock()
	if still {
		c.finish(d.hub.errResult("DEVICE_OFFLINE", map[string]any{"device": d.Name(), "timeout": c.OfflineWait.Seconds()}))
	}
}

func (d *Device) onEvent(sess *agentlink.Session, ev string, m map[string]any) {
	switch ev {
	case "ping":
		d.mu.Lock()
		n := d.lastN
		d.ackedN = n
		d.mu.Unlock()
		sess.Send(map[string]any{"event": "pong", "t": m["t"], "ack": n})
	case "result":
		id := agentlink.Uint(m, "id")
		d.mu.Lock()
		c := d.accepted[id]
		delete(d.accepted, id)
		d.mu.Unlock()
		if c == nil {
			return
		}
		delete(m, "event")
		delete(m, "id")
		r := d.hub.finishResult(d, c, Result(m))
		if c.Wait {
			c.finish(r)
		} else if c.Late != nil {
			c.Late(r)
		}
	case "state":
		if ready, ok := m["ready"].(map[string]any); ok {
			d.mu.Lock()
			d.hello.Ready = map[string]bool{}
			for k, v := range ready {
				b, _ := v.(bool)
				d.hello.Ready[k] = b
			}
			d.mu.Unlock()
		}
	case "notification":
		m["device"] = d.ID
		d.hub.onNotification(d, m)
	case "screen", "toast":
		m["device"] = d.ID
		d.hub.broadcast(ev, d.ID, m)
	}
}

// answer delivers the first response to the waiting client and unblocks the worker.
func (c *Call) answer(r Result) {
	c.markAnswered()
	c.finish(r)
}

func (c *Call) markAnswered() {
	c.answeredOnce.Do(func() { close(c.answered) })
}

func (d *Device) shutdown() {
	d.once.Do(func() {
		close(d.gone)
		d.mu.Lock()
		s := d.sess
		d.mu.Unlock()
		if s != nil {
			s.Close()
		}
	})
}

// finishResult renders error messages in the server language and adds context.
func (h *Hub) finishResult(d *Device, c *Call, r Result) Result {
	ok, _ := r["ok"].(bool)
	if ok {
		return r
	}
	code, _ := r["error"].(string)
	fields := copyMap(r)
	for _, k := range []string{"by", "value", "timeout", "package", "activity"} {
		if _, have := fields[k]; !have {
			if v, ok := c.Params[k]; ok {
				fields[k] = v
			}
		}
	}
	if _, have := fields["cmd"]; !have {
		fields["cmd"] = c.Cmd.Name
	}
	fields["device"] = d.Name()
	if code == "NOT_FOUND" || code == "MACRO_FAILED" {
		if s, ok := fields["screen"].(string); ok {
			fields["screen"] = strings.Replace(s, "/", " / ", 1)
		}
	}
	for k, v := range fields {
		if n, ok := v.(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				fields[k] = f
			}
		}
	}
	if def := h.spec.Error(code); def != nil {
		r["msg"] = h.spec.RenderError(code, h.Lang(), fields)
		r["retryable"] = def.Retryable
	}
	return r
}
