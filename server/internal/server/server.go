// Package server wires the hub to its listeners: the agent port, the client
// API, UDP discovery and an optional relay connection.
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/KnifeLemon/Droidline/server/internal/hub"
	"github.com/KnifeLemon/Droidline/server/internal/store"
	"github.com/KnifeLemon/Droidline/server/internal/wire"
	"github.com/KnifeLemon/Droidline/spec"
)

type Server struct {
	Hub   *hub.Hub
	Store *store.Store
	Log   *slog.Logger

	tlsCfg      *tls.Config
	agentLn     net.Listener
	clientLn    net.Listener
	udp         net.PacketConn
	AgentPort   int
	ClientPort  int
	clientLocal bool
	wg          sync.WaitGroup
}

// Start opens every listener. Close stops them.
func Start(st *store.Store, log *slog.Logger) (*Server, error) {
	sp, err := spec.Load()
	if err != nil {
		return nil, err
	}
	tlsCfg, fp, err := loadTLS(st)
	if err != nil {
		return nil, fmt.Errorf("tls certificate: %w", err)
	}
	s := &Server{Store: st, Log: log, tlsCfg: tlsCfg}

	s.agentLn, err = net.Listen("tcp", st.Config.Agent.Listen)
	if err != nil {
		return nil, fmt.Errorf("agent port %s: %w (is another droidline serve running?)", st.Config.Agent.Listen, err)
	}
	s.AgentPort = s.agentLn.Addr().(*net.TCPAddr).Port
	s.clientLn, err = net.Listen("tcp", st.Config.Client.Listen)
	if err != nil {
		s.agentLn.Close()
		return nil, fmt.Errorf("client port %s: %w", st.Config.Client.Listen, err)
	}
	s.ClientPort = s.clientLn.Addr().(*net.TCPAddr).Port
	s.clientLocal = s.clientLn.Addr().(*net.TCPAddr).IP.IsLoopback()

	h, err := hub.New(hub.Options{
		Store: st, Spec: sp, Log: log, TLSFP: fp,
		LANAddr: func() []string { return s.listenAddresses() },
	})
	if err != nil {
		s.agentLn.Close()
		s.clientLn.Close()
		return nil, err
	}
	h.SetPorts(s.AgentPort, s.ClientPort)
	s.Hub = h

	if st.Config.Discovery.Listen != "off" {
		if s.udp, err = net.ListenPacket("udp4", st.Config.Discovery.Listen); err != nil {
			log.Warn("discovery disabled; phones on this Wi-Fi must use the QR code", "err", err)
		} else {
			go serveDiscovery(s.udp, h, s.AgentPort, log)
		}
	}
	go s.acceptAgents()
	go s.acceptClients()
	if st.Config.Relay.URL != "" {
		go runRelay(context.Background(), s)
	}
	return s, nil
}

// listenAddresses is what phones are told to dial on the local network: every
// private IPv4 address when listening on all interfaces, else the bound one.
func (s *Server) listenAddresses() []string {
	ip := s.agentLn.Addr().(*net.TCPAddr).IP
	if ip.IsUnspecified() {
		return hub.LANAddresses(s.AgentPort)
	}
	return []string{fmt.Sprintf("tcp://%s", net.JoinHostPort(ip.String(), strconv.Itoa(s.AgentPort)))}
}

func (s *Server) Close() {
	s.agentLn.Close()
	s.clientLn.Close()
	if s.udp != nil {
		s.udp.Close()
	}
}

func (s *Server) acceptAgents() {
	httpLn := wire.NewChanListener(s.agentLn.Addr())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		transport := "ws"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			transport = "wss"
		}
		s.Hub.AcceptAgent(wire.NewWS(c, r.RemoteAddr, transport))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	go (&http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}).Serve(httpLn)

	for {
		c, err := s.agentLn.Accept()
		if err != nil {
			httpLn.Close()
			return
		}
		go s.routeAgent(c, httpLn, false)
	}
}

// routeAgent picks NDJSON, TLS or HTTP by the first byte; TLS is unwrapped once.
func (s *Server) routeAgent(c net.Conn, httpLn *wire.ChanListener, inTLS bool) {
	first, bc, err := wire.Sniff(c, 15*time.Second)
	if err != nil {
		c.Close()
		return
	}
	switch {
	case first == '{':
		transport := "tcp"
		if inTLS {
			transport = "tls"
		}
		s.Hub.AcceptAgent(wire.NewStream(bc, transport))
	case first == 0x16 && !inTLS:
		tc := tls.Server(bc, s.tlsCfg)
		tc.SetDeadline(time.Now().Add(15 * time.Second))
		if err := tc.Handshake(); err != nil {
			c.Close()
			return
		}
		tc.SetDeadline(time.Time{})
		s.routeAgent(tc, httpLn, true)
	case first >= 'A' && first <= 'Z':
		httpLn.Push(bc)
	default:
		c.Close()
	}
}

func (s *Server) acceptClients() {
	httpLn := wire.NewChanListener(s.clientLn.Addr())
	go (&http.Server{Handler: s.httpHandler(), ReadHeaderTimeout: 10 * time.Second}).Serve(httpLn)
	for {
		c, err := s.clientLn.Accept()
		if err != nil {
			httpLn.Close()
			return
		}
		go func() {
			first, bc, err := wire.Sniff(c, 30*time.Second)
			if err != nil {
				c.Close()
				return
			}
			if first == '{' {
				s.serveNDJSON(wire.NewStream(bc, "tcp"), s.needsAuth(c.RemoteAddr()))
				return
			}
			httpLn.Push(bc)
		}()
	}
}

func (s *Server) needsAuth(remote net.Addr) bool {
	if s.Store.Config.Client.RequireToken {
		return true
	}
	if ta, ok := remote.(*net.TCPAddr); ok {
		return !ta.IP.IsLoopback()
	}
	return true
}

// serveNDJSON runs one client connection: requests are queued in order and
// answered as they finish, possibly out of order across devices.
func (s *Server) serveNDJSON(lc wire.LineConn, needsAuth bool) {
	defer s.Hub.RecoverConn("client", lc.RemoteAddr())
	defer lc.Close()
	client := s.Hub.NewClient(func(m map[string]any) error { return lc.WriteLine(hub.MarshalLine(m)) }, needsAuth)
	defer client.Close()
	for {
		line, err := lc.ReadLine()
		if err != nil {
			return
		}
		if len(line) == 0 {
			continue
		}
		var req map[string]any
		if err := json.Unmarshal(line, &req); err != nil {
			lc.WriteLine(hub.MarshalLine(map[string]any{"ok": false, "error": "BAD_ARGS", "msg": "line is not a JSON object: " + err.Error()}))
			continue
		}
		id := req["id"]
		ch := client.Start(req)
		go func() {
			r := <-ch
			out := map[string]any{"id": id}
			for k, v := range r {
				out[k] = v
			}
			lc.WriteLine(hub.MarshalLine(out))
		}()
	}
}

func (s *Server) httpHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		s.runHTTP(w, r, map[string]any{"cmd": "devices"})
	})
	mux.HandleFunc("POST /devices/{device}/{cmd}", func(w http.ResponseWriter, r *http.Request) {
		req, ok := readBody(w, r)
		if !ok {
			return
		}
		req["cmd"] = r.PathValue("cmd")
		req["device"] = r.PathValue("device")
		s.runHTTP(w, r, req)
	})
	mux.HandleFunc("GET /devices/{device}/{file}", func(w http.ResponseWriter, r *http.Request) {
		format := map[string]string{"screenshot.jpg": "jpeg", "screenshot.jpeg": "jpeg", "screenshot.png": "png"}[r.PathValue("file")]
		if format == "" {
			http.NotFound(w, r)
			return
		}
		client := s.httpClient(r)
		defer client.Close()
		req := map[string]any{"cmd": "screenshot", "device": r.PathValue("device"), "format": format}
		if q := r.URL.Query().Get("quality"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				req["quality"] = float64(n)
			}
		}
		if sc := r.URL.Query().Get("scale"); sc != "" {
			if f, err := strconv.ParseFloat(sc, 64); err == nil {
				req["scale"] = f
			}
		}
		res := client.Exec(req)
		if ok, _ := res["ok"].(bool); !ok {
			writeResult(w, s.Hub, res)
			return
		}
		data, _ := res["data"].(string)
		img, err := decodeB64(data)
		if err != nil {
			http.Error(w, "bad image from phone", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/"+format)
		w.Write(img)
	})
	mux.HandleFunc("POST /server/{cmd}", func(w http.ResponseWriter, r *http.Request) {
		req, ok := readBody(w, r)
		if !ok {
			return
		}
		req["cmd"] = r.PathValue("cmd")
		s.runHTTP(w, r, req)
	})
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		opts := &websocket.AcceptOptions{OriginPatterns: s.Store.Config.Client.AllowedOrigins}
		if r.Header.Get("Origin") == "" {
			opts.InsecureSkipVerify = true
		}
		c, err := websocket.Accept(w, r, opts)
		if err != nil {
			return
		}
		s.serveNDJSON(wire.NewWS(c, r.RemoteAddr, "ws"), s.needsAuthHTTP(r))
	})
	return s.guard(mux)
}

// guard blocks browsers and DNS rebinding: a page you visit must not drive your phones.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && !contains(s.Store.Config.Client.AllowedOrigins, o) {
			http.Error(w, "browser origin not allowed; add it to client.allowed_origins", http.StatusForbidden)
			return
		}
		if s.clientLocal {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}
			if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
				http.Error(w, "host not allowed", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodPost {
			ct := r.Header.Get("Content-Type")
			if len(ct) < 16 || ct[:16] != "application/json" {
				http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) needsAuthHTTP(r *http.Request) bool {
	if s.Store.Config.Client.RequireToken {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func (s *Server) httpClient(r *http.Request) *hub.Client {
	c := s.Hub.NewClient(func(map[string]any) error { return nil }, s.needsAuthHTTP(r))
	if tok := bearer(r); tok != "" {
		c.Exec(map[string]any{"cmd": "auth", "token": tok})
	}
	return c
}

func (s *Server) runHTTP(w http.ResponseWriter, r *http.Request, req map[string]any) {
	client := s.httpClient(r)
	defer client.Close()
	writeResult(w, s.Hub, client.Exec(req))
}

func writeResult(w http.ResponseWriter, h *hub.Hub, res hub.Result) {
	status := http.StatusOK
	if ok, _ := res["ok"].(bool); !ok {
		status = h.HTTPStatus(fmt.Sprint(res["error"]))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(hub.MarshalLine(res))
	w.Write([]byte("\n"))
}

func readBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	req := map[string]any{}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, wire.MaxLine))
	if err := dec.Decode(&req); err != nil && !errors.Is(err, errEOF) {
		http.Error(w, "body must be a JSON object", http.StatusBadRequest)
		return nil, false
	}
	return req, true
}

func bearer(r *http.Request) string {
	const p = "Bearer "
	if a := r.Header.Get("Authorization"); len(a) > len(p) && a[:len(p)] == p {
		return a[len(p):]
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
