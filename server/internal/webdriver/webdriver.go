// Package webdriver speaks the W3C WebDriver protocol with Appium's Android
// extensions, so Appium clients, Appium Inspector, WebdriverIO and Robot Framework
// can drive phones through a running Droidline server. It is optional: it runs only
// while "droidline webdriver" runs.
package webdriver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/client"
)

// The W3C key that marks an element reference in requests and responses.
const elementKey = "element-6066-11e4-a52e-4f735466cecf"

type Bridge struct {
	cl       *client.Client
	version  string
	loopback bool

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	id       string
	device   string
	lease    string
	implicit float64 // seconds
	caps     map[string]any
	idle     time.Duration // appium:newCommandTimeout; 0 keeps the session forever
	last     time.Time

	mu       sync.Mutex
	elements map[string]map[string]any
}

func New(cl *client.Client, version string, loopback bool) *Bridge {
	b := &Bridge{cl: cl, version: version, loopback: loopback, sessions: map[string]*session{}}
	go b.reap()
	return b
}

// reap ends sessions that saw no command for their newCommandTimeout, as Appium does,
// so a crashed test does not keep its leased phone.
func (b *Bridge) reap() {
	for range time.Tick(5 * time.Second) {
		var done []*session
		b.mu.Lock()
		for id, s := range b.sessions {
			s.mu.Lock()
			idle := s.idle > 0 && time.Since(s.last) > s.idle
			s.mu.Unlock()
			if idle {
				delete(b.sessions, id)
				done = append(done, s)
			}
		}
		b.mu.Unlock()
		for _, s := range done {
			b.releaseSession(s)
		}
	}
}

type wdError struct {
	status int
	code   string
	msg    string
}

func (e *wdError) Error() string { return e.code + ": " + e.msg }

func errf(status int, code, format string, a ...any) *wdError {
	return &wdError{status, code, fmt.Sprintf(format, a...)}
}

var (
	errNoSuchElement = func(msg string) *wdError { return errf(404, "no such element", "%s", msg) }
	errInvalidArg    = func(msg string) *wdError { return errf(400, "invalid argument", "%s", msg) }
)

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Handler serves the protocol. Browsers and DNS rebinding are blocked the same way
// as on the server's own HTTP API.
func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern string, fn func(r *http.Request, s *session, body map[string]any) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if r.Method == http.MethodPost {
				_ = json.NewDecoder(r.Body).Decode(&body)
			}
			var s *session
			if sid := r.PathValue("sid"); sid != "" {
				b.mu.Lock()
				s = b.sessions[sid]
				b.mu.Unlock()
				if s == nil {
					reply(w, nil, errf(404, "invalid session id", "no session %s", sid))
					return
				}
				s.mu.Lock()
				s.last = time.Now()
				s.mu.Unlock()
			}
			v, err := fn(r, s, body)
			reply(w, v, err)
		})
	}
	for _, rt := range b.routes() {
		route(rt.pattern, rt.fn)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, errf(404, "unknown command", "%s %s is not supported by the Droidline bridge", r.Method, r.URL.Path))
	})
	return b.guard(mux)
}

func (b *Bridge) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			reply(w, nil, errf(403, "unknown error", "requests from web pages are not allowed"))
			return
		}
		if b.loopback {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}
			if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
				reply(w, nil, errf(403, "unknown error", "host not allowed"))
				return
			}
		}
		if r.Method == http.MethodPost && r.ContentLength > 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			reply(w, nil, errf(415, "invalid argument", "content-type must be application/json"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		we, ok := err.(*wdError)
		if !ok {
			we = errf(500, "unknown error", "%v", err)
		}
		w.WriteHeader(we.status)
		_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]any{"error": we.code, "message": we.msg, "stacktrace": ""}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"value": v})
}

// call sends one Droidline command for the session's phone and turns a failure into a WebDriver error.
func (b *Bridge) call(s *session, req map[string]any) (map[string]any, error) {
	if s != nil {
		if s.device != "" {
			req["device"] = s.device
		}
		if s.lease != "" {
			req["lease"] = s.lease
		}
	}
	res, err := b.cl.Call(req)
	if err != nil {
		return nil, errf(500, "unknown error", "%v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		msg := fmt.Sprint(res["msg"])
		switch res["error"] {
		case "NOT_FOUND":
			return res, errNoSuchElement(msg)
		case "BAD_ARGS":
			return res, errInvalidArg(msg)
		case "UNKNOWN_CMD":
			return res, errf(404, "unknown method", "%s", msg)
		}
		return res, errf(500, "unknown error", "%v: %s", res["error"], msg)
	}
	return res, nil
}

func (s *session) remember(node map[string]any) map[string]any {
	id := newID()
	s.mu.Lock()
	s.elements[id] = node
	s.mu.Unlock()
	return map[string]any{elementKey: id, "ELEMENT": id}
}

func (s *session) element(id string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.elements[id]; n != nil {
		return n, nil
	}
	return nil, errf(404, "no such element", "element %s is unknown in this session", id)
}

// query finds the element again by its exact bounds and class.
func query(node map[string]any) map[string]any {
	q := map[string]any{"bounds": node["bounds"]}
	if c, _ := node["class"].(string); c != "" {
		q["class"] = c
	}
	return q
}

func elementID(v any) string {
	m, _ := v.(map[string]any)
	if id, ok := m[elementKey].(string); ok {
		return id
	}
	id, _ := m["ELEMENT"].(string)
	return id
}
