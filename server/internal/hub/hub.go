// Package hub connects clients to phones: it owns the device registry,
// per-device command queues, pairing, events and server-side commands.
package hub

import (
	"crypto/ecdh"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/agentlink"
	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
	"github.com/KnifeLemon/Droidline/server/internal/store"
	"github.com/KnifeLemon/Droidline/server/internal/wire"
	"github.com/KnifeLemon/Droidline/spec"
)

// Version is set at release build time with -ldflags "-X .../hub.Version=x.y.z".
var Version = "0.1.0-dev"

const (
	pingInterval    = 25 * time.Second
	pendingPairTTL  = 5 * time.Minute
	enrollTTL       = 10 * time.Minute
	maxPendingPairs = 8
)

// Result is a response without its id: ok plus result or error fields.
type Result map[string]any

type Options struct {
	Store   *store.Store
	Spec    *spec.Spec
	Log     *slog.Logger
	TLSFP   string
	LANAddr func() []string
}

type Hub struct {
	spec     *spec.Spec
	store    *store.Store
	log      *slog.Logger
	serverID string
	key      *ecdh.PrivateKey
	tlsFP    string
	lanAddr  func() []string
	started  time.Time

	mu       sync.Mutex
	devices  map[string]*Device
	pending  map[string]*pendingPair
	enrolls  map[string]*enrollment
	clients  map[*Client]struct{}
	waiters  map[*notifWaiter]struct{}
	webhooks *webhookSender
	ports    [2]int
	leases   leaseTable
}

type pendingPair struct {
	acc     *agentlink.Accepted
	created time.Time
	approve chan string
}

type enrollment struct {
	token   []byte
	name    string
	expires time.Time
}

func New(o Options) (*Hub, error) {
	id, err := o.Store.ServerID(func() string { return dlcrypto.RandomID(10) })
	if err != nil {
		return nil, err
	}
	key, err := loadOrCreateKey(o.Store)
	if err != nil {
		return nil, err
	}
	h := &Hub{
		spec: o.Spec, store: o.Store, log: o.Log, serverID: id, key: key,
		tlsFP: o.TLSFP, lanAddr: o.LANAddr, started: time.Now(),
		devices: map[string]*Device{}, pending: map[string]*pendingPair{},
		enrolls: map[string]*enrollment{}, clients: map[*Client]struct{}{},
		waiters: map[*notifWaiter]struct{}{},
	}
	if h.lanAddr == nil {
		h.lanAddr = func() []string { return nil }
	}
	h.webhooks = newWebhookSender(h)
	for _, rec := range o.Store.Devices() {
		h.devices[rec.ID] = newDevice(h, rec)
	}
	return h, nil
}

func loadOrCreateKey(s *store.Store) (*ecdh.PrivateKey, error) {
	if hexd := s.Secret("server_key"); hexd != "" {
		d, err := dlcrypto.UnB64(hexd)
		if err != nil {
			return nil, fmt.Errorf("server key: %w", err)
		}
		return dlcrypto.PrivateKey(d)
	}
	k, err := dlcrypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	return k, s.SetSecret("server_key", dlcrypto.B64(k.Bytes()))
}

func (h *Hub) ServerID() string            { return h.serverID }
func (h *Hub) StaticKey() *ecdh.PrivateKey { return h.key }
func (h *Hub) PublicKey() string           { return dlcrypto.EncodePublic(h.key.PublicKey()) }
func (h *Hub) Name() string                { return h.store.Config.Name }
func (h *Hub) Lang() string                { return h.store.Config.Lang }
func (h *Hub) SetPorts(agent, client int)  { h.ports = [2]int{agent, client} }
func (h *Hub) CodePairingOpen() bool       { return h.store.Config.Pairing.Code }

func (h *Hub) DevicePub(id string) (*ecdh.PublicKey, bool) {
	rec, ok := h.store.Device(id)
	if !ok {
		return nil, false
	}
	pub, err := dlcrypto.PublicKey(rec.Pub)
	return pub, err == nil
}

func (h *Hub) EnrollToken(tid string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.enrolls[tid]
	if e == nil || time.Now().After(e.expires) {
		return nil, false
	}
	return e.token, true
}

// AcceptAgent handles one phone link until it closes.
func (h *Hub) AcceptAgent(conn wire.LineConn) {
	defer h.recoverConn("agent", conn.RemoteAddr())
	acc, err := agentlink.Accept(conn, h)
	if err != nil {
		h.log.Debug("agent handshake failed", "remote", conn.RemoteAddr(), "err", err)
		return
	}
	in := pump(acc.Session)
	switch acc.Mode {
	case "auth":
		h.mu.Lock()
		d := h.devices[acc.Hello.Device]
		h.mu.Unlock()
		if d == nil {
			acc.Session.Close()
			return
		}
		d.attach(acc.Session, acc.Hello, in)
	case "pair_qr":
		h.mu.Lock()
		e := h.enrolls[acc.TID]
		delete(h.enrolls, acc.TID)
		h.mu.Unlock()
		if e == nil {
			acc.Session.Close()
			return
		}
		h.completePairing(acc, e.name, in)
	case "pair_code":
		h.holdForCode(acc, in)
	}
}

type inbound struct {
	m   map[string]any
	err error
}

// pump is the only reader of a session for its whole life, so a session can
// move from "pending pair" to "attached" without two goroutines calling Recv.
func pump(s *agentlink.Session) <-chan inbound {
	ch := make(chan inbound, 64)
	go func() {
		defer close(ch)
		for {
			m, err := s.Recv()
			if err != nil {
				ch <- inbound{err: err}
				return
			}
			ch <- inbound{m: m}
		}
	}()
	return ch
}

func (h *Hub) holdForCode(acc *agentlink.Accepted, in <-chan inbound) {
	p := &pendingPair{acc: acc, created: time.Now(), approve: make(chan string, 1)}
	h.mu.Lock()
	h.expirePendingLocked()
	if len(h.pending) >= maxPendingPairs || h.pending[acc.SAS] != nil {
		h.mu.Unlock()
		acc.Session.Close()
		return
	}
	h.pending[acc.SAS] = p
	h.mu.Unlock()
	h.log.Info("a phone is waiting to pair; type the code it shows: droidline pair <code>",
		"model", acc.Hello.Model, "device", acc.Hello.Device, "remote", acc.Session.RemoteAddr())
	defer func() {
		h.mu.Lock()
		if h.pending[acc.SAS] == p {
			delete(h.pending, acc.SAS)
		}
		h.mu.Unlock()
	}()
	expire := time.NewTimer(pendingPairTTL)
	defer expire.Stop()
	for {
		select {
		case name := <-p.approve:
			h.completePairing(acc, name, in)
			return
		case msg, ok := <-in:
			if !ok || msg.err != nil {
				return
			}
			// Keep answering pings while the person walks to the PC.
			if agentlink.Str(msg.m, "event") == "ping" {
				acc.Session.Send(map[string]any{"event": "pong", "t": msg.m["t"], "ack": 0})
			}
		case <-expire.C:
			acc.Session.Close()
			return
		}
	}
}

func (h *Hub) completePairing(acc *agentlink.Accepted, name string, in <-chan inbound) {
	name = h.uniqueName(name, acc.Hello)
	if err := acc.Session.Send(map[string]any{"event": "paired", "name": name}); err != nil {
		acc.Session.Close()
		return
	}
	h.register(acc, name, in)
}

func (h *Hub) register(acc *agentlink.Accepted, name string, in <-chan inbound) {
	now := time.Now().Unix()
	rec := store.DeviceRecord{
		ID: acc.Hello.Device, Name: name, Pub: acc.PhonePub, Model: acc.Hello.Model,
		Manufacturer: acc.Hello.Manufacturer, SDK: acc.Hello.SDK, Release: acc.Hello.Release,
		Agent: acc.Hello.Agent, PairedAt: now, LastSeen: now,
	}
	if err := h.store.PutDevice(rec); err != nil {
		h.log.Error("saving paired device", "err", err)
		acc.Session.Close()
		return
	}
	h.mu.Lock()
	d := h.devices[rec.ID]
	if d == nil {
		d = newDevice(h, rec)
		h.devices[rec.ID] = d
	} else {
		d.setRecord(rec)
	}
	h.mu.Unlock()
	h.log.Info("paired", "device", rec.ID, "name", name, "model", rec.Model)
	d.attach(acc.Session, acc.Hello, in)
}

var nameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func (h *Hub) uniqueName(want string, hello agentlink.Hello) string {
	base := strings.TrimSpace(want)
	if base == "" {
		model := strings.ToLower(nameUnsafe.ReplaceAllString(hello.Model, "-"))
		if model == "" {
			model = "phone"
		}
		base = model + "-" + hello.Device[:min(4, len(hello.Device))]
	}
	taken := map[string]bool{}
	for _, r := range h.store.Devices() {
		if r.ID != hello.Device {
			taken[r.Name] = true
		}
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

func (h *Hub) expirePendingLocked() {
	for code, p := range h.pending {
		if time.Since(p.created) > pendingPairTTL {
			delete(h.pending, code)
		}
	}
	for tid, e := range h.enrolls {
		if time.Now().After(e.expires) {
			delete(h.enrolls, tid)
		}
	}
}

// PendingPairs lists phones waiting for a code, without revealing the codes.
func (h *Hub) PendingPairs() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expirePendingLocked()
	var out []map[string]any
	for _, p := range h.pending {
		out = append(out, map[string]any{
			"device": p.acc.Hello.Device, "model": p.acc.Hello.Model,
			"remote": p.acc.Session.RemoteAddr(), "waiting_s": int(time.Since(p.created).Seconds()),
		})
	}
	return out
}

// approve accepts the phone showing code. The phone displays its code right
// after the handshake, a moment before the server has read its hello, so a
// quick operator gets a short grace period.
func (h *Hub) approve(code, name string) (string, bool) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		p := h.pending[code]
		if p != nil {
			delete(h.pending, code)
		}
		h.mu.Unlock()
		if p != nil {
			p.approve <- name
			return p.acc.Hello.Device, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// NewEnrollment creates a single-use QR token and returns the pairing URI.
func (h *Hub) NewEnrollment(name string) (string, time.Time) {
	tid := dlcrypto.RandomID(8)
	tok, _ := dlcrypto.UnB64(dlcrypto.RandomToken())
	exp := time.Now().Add(enrollTTL)
	h.mu.Lock()
	h.expirePendingLocked()
	h.enrolls[tid] = &enrollment{token: tok, name: name, expires: exp}
	h.mu.Unlock()
	return h.pairingURI(tid, tok, exp), exp
}

func (h *Hub) deviceByRef(ref string) *Device {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := h.devices[ref]; d != nil {
		return d
	}
	for _, d := range h.devices {
		if d.Name() == ref {
			return d
		}
	}
	return nil
}

func (h *Hub) sortedDevices() []*Device {
	h.mu.Lock()
	out := make([]*Device, 0, len(h.devices))
	for _, d := range h.devices {
		out = append(out, d)
	}
	h.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].pairedAt() < out[j].pairedAt() })
	return out
}

// pickDevice resolves the envelope's device field, or the implicit single device.
func (h *Hub) pickDevice(ref string) (*Device, Result) {
	if ref != "" && ref != "_" {
		if d := h.deviceByRef(ref); d != nil {
			return d, nil
		}
		return nil, h.errResult("DEVICE_NOT_FOUND", map[string]any{"device": ref})
	}
	all := h.sortedDevices()
	var online []*Device
	for _, d := range all {
		if d.Online() {
			online = append(online, d)
		}
	}
	switch {
	case len(online) == 1:
		return online[0], nil
	case len(online) == 0 && len(all) == 1:
		return all[0], nil
	case len(all) == 0:
		r := h.errResult("DEVICE_NOT_FOUND", map[string]any{"device": "(none)"})
		r["msg"] = h.noDevicesMsg()
		return nil, r
	}
	cands := online
	if len(cands) == 0 {
		cands = all
	}
	names := make([]string, len(cands))
	for i, d := range cands {
		names[i] = d.Name()
	}
	return nil, h.errResult("DEVICE_AMBIGUOUS", map[string]any{"count": len(cands), "devices": names})
}

func (h *Hub) noDevicesMsg() string {
	switch h.Lang() {
	case "ko":
		return "등록된 폰이 없습니다. droidline pair 로 폰을 먼저 등록하세요"
	case "zh":
		return "还没有配对的手机。请先运行 droidline pair 配对手机"
	}
	return "No phone is paired yet. Run droidline pair first"
}

func (h *Hub) errResult(code string, fields map[string]any) Result {
	r := Result{"ok": false, "error": code}
	for k, v := range fields {
		r[k] = v
	}
	r["msg"] = h.spec.RenderError(code, h.Lang(), copyMap(fields))
	if def := h.spec.Error(code); def != nil {
		r["retryable"] = def.Retryable
	}
	return r
}

func (h *Hub) revoke(d *Device) error {
	h.mu.Lock()
	delete(h.devices, d.ID)
	h.mu.Unlock()
	d.shutdown()
	return h.store.RemoveDevice(d.ID)
}

// broadcast sends an event to every client subscribed to its kind and device.
func (h *Hub) broadcast(kind, device string, ev map[string]any) {
	h.mu.Lock()
	var targets []*Client
	for c := range h.clients {
		if c.wants(kind, device) {
			targets = append(targets, c)
		}
	}
	h.mu.Unlock()
	for _, c := range targets {
		c.send(ev)
	}
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func jsonNumber(n int64) json.Number { return json.Number(fmt.Sprint(n)) }

// recoverConn keeps one misbehaving connection from stopping the server; the
// stack goes to the log so the bug can be reported.
func (h *Hub) recoverConn(kind, remote string) {
	if r := recover(); r != nil {
		h.log.Error("connection handler crashed; please report this", "kind", kind, "remote", remote, "panic", r, "stack", string(debug.Stack()))
	}
}

// RecoverConn is recoverConn for handlers outside this package.
func (h *Hub) RecoverConn(kind, remote string) { h.recoverConn(kind, remote) }

// HTTPStatus maps an error code to the status the local HTTP API returns.
func (h *Hub) HTTPStatus(code string) int {
	if def := h.spec.Error(code); def != nil && def.HTTP != 0 {
		return def.HTTP
	}
	return 500
}
