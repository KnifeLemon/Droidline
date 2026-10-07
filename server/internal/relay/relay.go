// Package relay forwards lines between a Droidline server and its phones when
// both sit behind NAT (PROTOCOL.md section 6). It never sees plaintext: past
// the handshake every line is an envelope only the two ends can open.
package relay

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
)

const (
	maxLine      = 1 << 20
	closeReplace = 4000
	closeFlood   = 4008
	ratePerSec   = 50
	rateBurst    = 200
)

var validID = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

type Relay struct {
	Token string
	Log   *slog.Logger

	mu    sync.Mutex
	rooms map[string]*room
}

type room struct {
	server  *peer
	devices map[string]*peer
}

type peer struct {
	c      *websocket.Conn
	wmu    sync.Mutex
	enroll bool
	ctx    context.Context
	cancel context.CancelFunc
}

func (p *peer) send(b []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()
	return p.c.Write(ctx, websocket.MessageText, b)
}

func (p *peer) close(code websocket.StatusCode, reason string) {
	p.c.Close(code, reason)
	p.cancel()
}

func New(token string, log *slog.Logger) *Relay {
	return &Relay{Token: token, Log: log, rooms: map[string]*room{}}
}

func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /v1/server", r.serveServer)
	mux.HandleFunc("GET /v1/device", r.serveDevice)
	return mux
}

func bearer(req *http.Request) string {
	a := req.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(a, "Bearer "); ok {
		return t
	}
	return ""
}

func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

func (r *Relay) roomFor(server string) *room {
	rm := r.rooms[server]
	if rm == nil {
		rm = &room{devices: map[string]*peer{}}
		r.rooms[server] = rm
	}
	return rm
}

func (r *Relay) serveServer(w http.ResponseWriter, req *http.Request) {
	server := req.URL.Query().Get("server")
	if !isUpgrade(req) {
		http.Error(w, "websocket required", http.StatusUpgradeRequired)
		return
	}
	if !validID.MatchString(server) {
		http.Error(w, "bad server id", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(bearer(req)), []byte(r.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c.SetReadLimit(2 * maxLine)
	ctx, cancel := context.WithCancel(context.Background())
	p := &peer{c: c, ctx: ctx, cancel: cancel}

	r.mu.Lock()
	rm := r.roomFor(server)
	old := rm.server
	rm.server = p
	var online []string
	for id := range rm.devices {
		online = append(online, id)
	}
	r.mu.Unlock()
	if old != nil {
		old.close(closeReplace, "replaced by a newer server connection")
	}
	r.Log.Info("server connected", "server", server)
	for _, id := range online {
		p.send(frame(id, "open", ""))
	}

	defer func() {
		r.mu.Lock()
		if rm.server == p {
			rm.server = nil
		}
		r.gcLocked(server)
		r.mu.Unlock()
		p.cancel()
		r.Log.Info("server disconnected", "server", server)
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			p.close(websocket.StatusUnsupportedData, "binary frames are not part of the protocol")
			return
		}
		var f struct {
			Device string `json:"device"`
			Line   string `json:"line"`
			Close  bool   `json:"close"`
		}
		if json.Unmarshal(data, &f) != nil || f.Device == "" {
			continue
		}
		if len(f.Line) > maxLine {
			p.close(websocket.StatusMessageTooBig, "line exceeds 1 MiB")
			return
		}
		r.mu.Lock()
		d := rm.devices[f.Device]
		r.mu.Unlock()
		if d == nil {
			continue
		}
		if f.Line != "" {
			d.send([]byte(f.Line))
		}
		if f.Close {
			d.close(websocket.StatusNormalClosure, "closed by server")
		}
	}
}

func (r *Relay) serveDevice(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	server, device := q.Get("server"), q.Get("device")
	if !isUpgrade(req) {
		http.Error(w, "websocket required", http.StatusUpgradeRequired)
		return
	}
	if !validID.MatchString(server) || !validID.MatchString(device) {
		http.Error(w, "bad server or device id", http.StatusBadRequest)
		return
	}
	ticket := bearer(req)
	enroll := false
	if exp := q.Get("expiry"); exp != "" {
		e, err := strconv.ParseInt(exp, 10, 64)
		if err != nil || !dlcrypto.TicketEqual(ticket, dlcrypto.EnrollTicket(r.Token, server, e)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if time.Now().Unix() > e {
			http.Error(w, "ticket expired", http.StatusUnauthorized)
			return
		}
		enroll = true
	} else if !dlcrypto.TicketEqual(ticket, dlcrypto.DeviceTicket(r.Token, server, device)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.mu.Lock()
	if cur := r.roomFor(server).devices[device]; enroll && cur != nil && !cur.enroll {
		r.mu.Unlock()
		http.Error(w, "device already connected", http.StatusConflict)
		return
	}
	r.mu.Unlock()

	c, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c.SetReadLimit(maxLine)
	ctx, cancel := context.WithCancel(context.Background())
	p := &peer{c: c, enroll: enroll, ctx: ctx, cancel: cancel}

	r.mu.Lock()
	rm := r.roomFor(server)
	old := rm.devices[device]
	rm.devices[device] = p
	srv := rm.server
	r.mu.Unlock()
	if old != nil {
		old.close(closeReplace, "replaced by a newer connection")
	}
	if srv != nil {
		srv.send(frame(device, "open", ""))
	}

	defer func() {
		r.mu.Lock()
		mine := rm.devices[device] == p
		if mine {
			delete(rm.devices, device)
		}
		srv := rm.server
		r.gcLocked(server)
		r.mu.Unlock()
		p.cancel()
		if mine && srv != nil {
			srv.send(frame(device, "close", ""))
		}
	}()

	tokens, last := float64(rateBurst), time.Now()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			p.close(websocket.StatusUnsupportedData, "binary frames are not part of the protocol")
			return
		}
		now := time.Now()
		tokens = min(rateBurst, tokens+now.Sub(last).Seconds()*ratePerSec)
		last = now
		if tokens < 1 {
			p.close(closeFlood, "rate limit")
			return
		}
		tokens--
		r.mu.Lock()
		srv := rm.server
		r.mu.Unlock()
		// With no server connected the line is dropped; the phone resends it on resume.
		if srv != nil {
			srv.send(frame(device, "", string(data)))
		}
	}
}

func (r *Relay) gcLocked(server string) {
	if rm := r.rooms[server]; rm != nil && rm.server == nil && len(rm.devices) == 0 {
		delete(r.rooms, server)
	}
}

func frame(device, event, line string) []byte {
	m := map[string]string{"device": device}
	if event != "" {
		m["event"] = event
	}
	if line != "" {
		m["line"] = line
	}
	b, _ := json.Marshal(m)
	return b
}
