// Package fakeagent is a phone simulator that speaks the real agent protocol
// over a tiny fake app, so servers, SDKs and CI can run without a device.
package fakeagent

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/KnifeLemon/Droidline/server/internal/agentlink"
	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
	"github.com/KnifeLemon/Droidline/server/internal/wire"
)

type Pairing struct {
	ServerID    string
	ServerPub   string
	Addr        string
	RelayURL    string `json:",omitempty"`
	RelayTicket string `json:",omitempty"`
}

type Agent struct {
	DeviceID string
	Model    string
	Log      *slog.Logger
	// OnSAS is called with the 6-digit code during code pairing.
	OnSAS func(code string)

	key  *ecdh.PrivateKey
	boot string

	mu       sync.Mutex
	pairing  *Pairing
	sess     *agentlink.Session
	n        uint64
	outbox   []outMsg
	cache    map[uint64]map[string]any
	running  map[uint64]bool
	ui       *phoneUI
	offline  chan struct{}
	allow    map[string]bool
	stopping bool
	viaRelay bool
}

// UseRelay makes later connections go through the relay from the config event.
func (a *Agent) UseRelay(on bool) {
	a.mu.Lock()
	a.viaRelay = on
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

func (a *Agent) dial(p *Pairing) (wire.LineConn, error) {
	a.mu.Lock()
	relay := a.viaRelay
	a.mu.Unlock()
	if !relay {
		conn, err := net.DialTimeout("tcp", p.Addr, 5*time.Second)
		if err != nil {
			return nil, err
		}
		return wire.NewStream(conn, "tcp"), nil
	}
	if p.RelayURL == "" {
		return nil, errors.New("no relay address received yet")
	}
	u := strings.Replace(strings.TrimRight(p.RelayURL, "/"), "http", "ws", 1) +
		"/v1/device?server=" + p.ServerID + "&device=" + a.DeviceID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + p.RelayTicket}}})
	if err != nil {
		return nil, err
	}
	return wire.NewWS(c, u, "relay"), nil
}

type outMsg struct {
	n   uint64
	msg map[string]any
}

func New(deviceID, model string, log *slog.Logger) (*Agent, error) {
	k, err := dlcrypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	if deviceID == "" {
		deviceID = dlcrypto.RandomID(8)
	}
	return &Agent{
		DeviceID: deviceID, Model: model, Log: log, key: k, boot: dlcrypto.RandomID(6),
		cache: map[uint64]map[string]any{}, running: map[uint64]bool{},
		ui: newPhoneUI(), allow: map[string]bool{},
	}, nil
}

// Restart simulates the app process dying: a new boot id and an empty cache.
func (a *Agent) Restart() {
	a.mu.Lock()
	a.boot = dlcrypto.RandomID(6)
	a.n = 0
	a.outbox = nil
	a.cache = map[uint64]map[string]any{}
	a.running = map[uint64]bool{}
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

func (a *Agent) Paired() *Pairing {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pairing
}

// PairCode pairs by comparing the 6-digit code; the server operator approves it.
func (a *Agent) PairCode(addr, serverPub string) error {
	return a.pair(addr, serverPub, "pair_code", "", nil)
}

// PairQR pairs using a droidline://pair URI.
func (a *Agent) PairQR(uri string) error {
	u, err := url.Parse(uri)
	if err != nil {
		return err
	}
	q := u.Query()
	tid, tok, ok := strings.Cut(q.Get("t"), ".")
	if !ok {
		return errors.New("pairing uri has no token")
	}
	token, err := dlcrypto.UnB64(tok)
	if err != nil {
		return err
	}
	// Try addresses in order like a phone does; the first one that answers wins.
	for _, ad := range strings.Split(q.Get("a"), ",") {
		if !strings.HasPrefix(ad, "tcp://") {
			continue
		}
		addr := strings.TrimPrefix(ad, "tcp://")
		if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
			c.Close()
			return a.pair(addr, q.Get("k"), "pair_qr", tid, token)
		}
	}
	return errors.New("no address in the pairing uri answered")
}

func (a *Agent) pair(addr, serverPub, mode, tid string, token []byte) error {
	pub, err := dlcrypto.PublicKey(serverPub)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	lc := wire.NewStream(conn, "tcp")
	sess, sas, serverID, err := a.handshake(lc, pub, mode, tid, token)
	if err != nil {
		return err
	}
	if mode == "pair_code" && a.OnSAS != nil {
		a.OnSAS(sas)
	}
	for {
		m, err := sess.Recv()
		if err != nil {
			return fmt.Errorf("pairing ended: %w", err)
		}
		if agentlink.Str(m, "event") == "paired" {
			break
		}
	}
	a.mu.Lock()
	a.pairing = &Pairing{ServerID: serverID, ServerPub: serverPub, Addr: addr}
	a.mu.Unlock()
	return a.serve(sess)
}

func (a *Agent) handshake(lc wire.LineConn, serverPub *ecdh.PublicKey, mode, tid string, token []byte) (*agentlink.Session, string, string, error) {
	eph, _ := dlcrypto.GenerateKey()
	hs := map[string]any{"hs": 1, "proto": agentlink.Proto, "mode": mode, "device": a.DeviceID,
		"eph": dlcrypto.EncodePublic(eph.PublicKey()), "nonce": dlcrypto.B64(dlcrypto.Nonce())}
	if mode != "auth" {
		hs["pub"] = dlcrypto.EncodePublic(a.key.PublicKey())
	}
	if tid != "" {
		hs["tid"] = tid
	}
	line1, _ := json.Marshal(hs)
	if err := lc.WriteLine(line1); err != nil {
		return nil, "", "", err
	}
	line2, err := lc.ReadLine()
	if err != nil {
		return nil, "", "", err
	}
	var rep struct {
		Status, Server, Eph string
	}
	json.Unmarshal(line2, &rep)
	if rep.Status != "ok" {
		lc.Close()
		return nil, "", "", fmt.Errorf("server said %s", rep.Status)
	}
	srvEph, err := dlcrypto.PublicKey(rep.Eph)
	if err != nil {
		return nil, "", "", err
	}
	keys, err := dlcrypto.Derive(line1, line2, eph, srvEph, a.key, serverPub, token)
	if err != nil {
		return nil, "", "", err
	}
	sess, err := agentlink.NewClientSession(lc, keys)
	if err != nil {
		return nil, "", "", err
	}
	a.mu.Lock()
	boot := a.boot
	a.mu.Unlock()
	err = sess.Send(map[string]any{
		"event": "hello", "device": a.DeviceID, "boot": boot, "model": a.Model, "manufacturer": "droidline",
		"sdk": 34, "release": "14", "agent": "0.1.0-fake", "route": routeOf(lc), "lang": "en",
		"ready": map[string]bool{"a11y": true, "ime": true, "vpn": true, "notif": true, "capture": true},
	})
	return sess, keys.SAS, rep.Server, err
}

// Run keeps the agent connected, reconnecting with backoff, until Stop.
func (a *Agent) Run() error {
	p := a.Paired()
	if p == nil {
		return errors.New("not paired")
	}
	pub, err := dlcrypto.PublicKey(p.ServerPub)
	if err != nil {
		return err
	}
	backoff := time.Second
	for {
		a.mu.Lock()
		stop, gate := a.stopping, a.offline
		a.mu.Unlock()
		if stop {
			return nil
		}
		if gate != nil {
			<-gate
		}
		lc, err := a.dial(a.Paired())
		if err == nil {
			sess, _, _, herr := a.handshake(lc, pub, "auth", "", nil)
			if herr == nil {
				backoff = time.Second
				a.serve(sess)
				continue
			}
			err = herr
		}
		a.Log.Debug("fake agent reconnect", "err", err, "in", backoff)
		time.Sleep(backoff)
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (a *Agent) Stop() {
	a.mu.Lock()
	a.stopping = true
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

// GoOffline drops the link and blocks reconnects for d, like airplane mode.
func (a *Agent) GoOffline(d time.Duration) {
	gate := make(chan struct{})
	a.mu.Lock()
	a.offline = gate
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		s.Close()
	}
	time.AfterFunc(d, func() {
		a.mu.Lock()
		a.offline = nil
		a.mu.Unlock()
		close(gate)
	})
}

func (a *Agent) serve(sess *agentlink.Session) error {
	a.mu.Lock()
	a.sess = sess
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.sess == sess {
			a.sess = nil
		}
		a.mu.Unlock()
		sess.Close()
	}()
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				sess.Send(map[string]any{"event": "ping", "t": time.Now().UnixMilli()})
			case <-done:
				return
			}
		}
	}()
	cmds := make(chan map[string]any, 64)
	defer close(cmds)
	// One executor, so commands run in arrival order as on a real phone.
	go func() {
		for m := range cmds {
			a.exec(m)
		}
	}()
	for {
		m, err := sess.Recv()
		if err != nil {
			return err
		}
		switch agentlink.Str(m, "event") {
		case "welcome":
			a.replay(agentlink.Uint(m, "ack"))
			continue
		case "ack":
			a.trim(agentlink.Uint(m, "n"))
			continue
		case "pong":
			a.trim(agentlink.Uint(m, "ack"))
			continue
		case "config":
			a.mu.Lock()
			if r, ok := m["relay"].(map[string]any); ok && a.pairing != nil {
				p := *a.pairing
				p.RelayURL, _ = r["url"].(string)
				p.RelayTicket, _ = r["ticket"].(string)
				a.pairing = &p
			}
			a.mu.Unlock()
			continue
		case "paired":
			continue
		}
		if agentlink.Str(m, "cmd") != "" {
			cmds <- m
		}
	}
}

// emit numbers a message, keeps it for resume, and sends it if connected.
func (a *Agent) emit(msg map[string]any) {
	a.mu.Lock()
	a.n++
	msg["n"] = a.n
	a.outbox = append(a.outbox, outMsg{a.n, msg})
	if len(a.outbox) > 2000 {
		a.outbox = a.outbox[len(a.outbox)-2000:]
	}
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		s.Send(msg)
	}
}

func (a *Agent) replay(ack uint64) {
	a.trim(ack)
	a.mu.Lock()
	pending := append([]outMsg(nil), a.outbox...)
	s := a.sess
	a.mu.Unlock()
	for _, m := range pending {
		if s != nil {
			s.Send(m.msg)
		}
	}
}

func (a *Agent) trim(ack uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := 0
	for i < len(a.outbox) && a.outbox[i].n <= ack {
		i++
	}
	a.outbox = a.outbox[i:]
}

func (a *Agent) exec(m map[string]any) {
	id := agentlink.Uint(m, "id")
	a.mu.Lock()
	if cached, ok := a.cache[id]; ok {
		a.mu.Unlock()
		a.emit(copyMsg(cached))
		return
	}
	if a.running[id] {
		a.mu.Unlock()
		return
	}
	a.running[id] = true
	a.mu.Unlock()

	name := agentlink.Str(m, "cmd")
	cuts := cutsNetwork(name, m)
	if cuts {
		a.emit(map[string]any{"id": id, "ok": true, "accepted": true})
	}
	res := a.ui.run(a, name, m)
	res["id"] = id
	a.mu.Lock()
	delete(a.running, id)
	a.cache[id] = copyMsg(res)
	a.mu.Unlock()
	if cuts {
		res["event"] = "result"
		// Real phones lose the link while the steps run; the result waits in the outbox.
		a.GoOffline(1500 * time.Millisecond)
		a.emit(res)
		return
	}
	a.emit(res)
}

func cutsNetwork(name string, m map[string]any) bool {
	v, _ := m["cuts_network"].(bool)
	return name == "batch" && v
}

// PostNotification simulates an app posting a notification on the phone.
func (a *Agent) PostNotification(pkg, title, text string) {
	key := fmt.Sprintf("0|%s|%d|null|10123", pkg, time.Now().UnixNano()%100000)
	n := map[string]any{"key": key, "package": pkg, "title": title, "text": text,
		"time": time.Now().UnixMilli(), "actions": []string{"reply"}}
	a.ui.addNotification(n)
	a.mu.Lock()
	allowed := a.allow[pkg]
	a.mu.Unlock()
	if allowed {
		ev := copyMsg(n)
		ev["event"] = "notification"
		a.emit(ev)
	}
}

func copyMsg(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Saved is what the simulator keeps on disk to reconnect after a restart.
type Saved struct {
	DeviceID string   `json:"device"`
	Model    string   `json:"model"`
	Key      string   `json:"key"`
	Pairing  *Pairing `json:"pairing"`
}

func (a *Agent) Save() Saved {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Saved{DeviceID: a.DeviceID, Model: a.Model, Key: dlcrypto.B64(a.key.Bytes()), Pairing: a.pairing}
}

func Restore(s Saved, log *slog.Logger) (*Agent, error) {
	a, err := New(s.DeviceID, s.Model, log)
	if err != nil {
		return nil, err
	}
	d, err := dlcrypto.UnB64(s.Key)
	if err != nil {
		return nil, err
	}
	if a.key, err = dlcrypto.PrivateKey(d); err != nil {
		return nil, err
	}
	a.pairing = s.Pairing
	return a, nil
}

// Discover broadcasts for a Droidline server on UDP 8778 and returns the first answer.
func Discover(timeout time.Duration) (addr, pub, name string, err error) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return "", "", "", err
	}
	defer pc.Close()
	q := []byte(`{"droidline":"discover","v":1}`)
	for _, target := range []string{"255.255.255.255:8778", "127.0.0.1:8778"} {
		if ua, err := net.ResolveUDPAddr("udp4", target); err == nil {
			pc.WriteTo(q, ua)
		}
	}
	pc.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 2048)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return "", "", "", errors.New("no Droidline server answered on UDP 8778; is droidline serve running on this network?")
		}
		var r struct {
			Droidline, Server, Name, Pub string
			Port                         int
		}
		if json.Unmarshal(buf[:n], &r) != nil || r.Droidline != "here" {
			continue
		}
		host, _, _ := net.SplitHostPort(from.String())
		return net.JoinHostPort(host, fmt.Sprint(r.Port)), r.Pub, r.Name, nil
	}
}

func routeOf(lc wire.LineConn) string {
	if lc.Transport() == "relay" {
		return "relay"
	}
	return "lan"
}

// DuplicateLink opens a second link while the first stays open, like a phone
// whose mobile connection died without the server noticing.
func (a *Agent) DuplicateLink() error {
	p := a.Paired()
	pub, err := dlcrypto.PublicKey(p.ServerPub)
	if err != nil {
		return err
	}
	lc, err := a.dial(p)
	if err != nil {
		return err
	}
	sess, _, _, err := a.handshake(lc, pub, "auth", "", nil)
	if err != nil {
		return err
	}
	go a.serve(sess)
	return nil
}
