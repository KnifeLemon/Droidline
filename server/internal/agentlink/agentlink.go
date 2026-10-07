// Package agentlink is the server side of the phone link: the two-line
// handshake and the encrypted session (PROTOCOL.md 4.4-4.5).
package agentlink

import (
	"bytes"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
	"github.com/KnifeLemon/Droidline/server/internal/wire"
)

const (
	Proto   = 1
	maxBlob = 512 << 10
)

// Authority answers the questions the handshake needs about paired devices.
type Authority interface {
	ServerID() string
	StaticKey() *ecdh.PrivateKey
	DevicePub(device string) (*ecdh.PublicKey, bool)
	EnrollToken(tid string) ([]byte, bool)
	CodePairingOpen() bool
}

type Hello struct {
	Device       string          `json:"device"`
	Boot         string          `json:"boot"`
	Model        string          `json:"model"`
	Manufacturer string          `json:"manufacturer"`
	SDK          int             `json:"sdk"`
	Release      string          `json:"release"`
	Agent        string          `json:"agent"`
	Route        string          `json:"route"`
	Lang         string          `json:"lang"`
	Ready        map[string]bool `json:"ready"`
}

type Accepted struct {
	Session  *Session
	Mode     string
	PhonePub string
	TID      string
	SAS      string
	Hello    Hello
}

type hsClient struct {
	HS     int    `json:"hs"`
	Proto  int    `json:"proto"`
	Mode   string `json:"mode"`
	Device string `json:"device"`
	Eph    string `json:"eph"`
	Nonce  string `json:"nonce"`
	Pub    string `json:"pub"`
	TID    string `json:"tid"`
}

// Accept runs the handshake on a fresh link and reads the phone's hello.
func Accept(conn wire.LineConn, auth Authority) (*Accepted, error) {
	line1, err := readWithin(conn, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	var hs hsClient
	if err := json.Unmarshal(line1, &hs); err != nil || hs.HS != 1 {
		return nil, errors.New("handshake: first line is not a Droidline hello")
	}
	reject := func(status string) (*Accepted, error) {
		b, _ := json.Marshal(map[string]any{"hs": 1, "proto": Proto, "server": auth.ServerID(), "status": status})
		conn.WriteLine(b)
		conn.Close()
		return nil, fmt.Errorf("handshake from %s rejected: %s", hs.Device, status)
	}
	if hs.Proto != Proto {
		return reject("bad_proto")
	}
	if !validID(hs.Device) {
		return reject("unknown_device")
	}
	ephPeer, err := dlcrypto.PublicKey(hs.Eph)
	if err != nil {
		return reject("bad_proto")
	}
	var staticPeer *ecdh.PublicKey
	var token []byte
	switch hs.Mode {
	case "auth":
		pub, ok := auth.DevicePub(hs.Device)
		if !ok {
			return reject("unknown_device")
		}
		staticPeer = pub
	case "pair_qr":
		tok, ok := auth.EnrollToken(hs.TID)
		if !ok {
			return reject("pairing_closed")
		}
		token = tok
		if staticPeer, err = dlcrypto.PublicKey(hs.Pub); err != nil {
			return reject("bad_proto")
		}
	case "pair_code":
		if !auth.CodePairingOpen() {
			return reject("pairing_closed")
		}
		if staticPeer, err = dlcrypto.PublicKey(hs.Pub); err != nil {
			return reject("bad_proto")
		}
	default:
		return reject("bad_proto")
	}

	eph, err := dlcrypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	line2, _ := json.Marshal(map[string]any{
		"hs": 1, "proto": Proto, "server": auth.ServerID(), "status": "ok",
		"eph": dlcrypto.EncodePublic(eph.PublicKey()), "nonce": dlcrypto.B64(dlcrypto.Nonce()),
	})
	if err := conn.WriteLine(line2); err != nil {
		return nil, err
	}
	keys, err := dlcrypto.Derive(line1, line2, eph, ephPeer, auth.StaticKey(), staticPeer, token)
	if err != nil {
		conn.Close()
		return nil, err
	}
	sess, err := newSession(conn, keys.Down, keys.Up)
	if err != nil {
		conn.Close()
		return nil, err
	}
	sess.DeviceID = hs.Device

	first, err := sess.recvWithin(15 * time.Second)
	if err != nil {
		sess.Close()
		// In auth mode this means the phone does not hold the paired key.
		return nil, fmt.Errorf("device %s failed to authenticate: %w", hs.Device, err)
	}
	if Str(first, "event") != "hello" {
		sess.Close()
		return nil, errors.New("first encrypted message was not hello")
	}
	var hello Hello
	raw, _ := json.Marshal(first)
	json.Unmarshal(raw, &hello)
	hello.Device = hs.Device
	return &Accepted{Session: sess, Mode: hs.Mode, PhonePub: hs.Pub, TID: hs.TID, SAS: keys.SAS, Hello: hello}, nil
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func readWithin(conn wire.LineConn, d time.Duration) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		b, err := conn.ReadLine()
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(d):
		conn.Close()
		return nil, errors.New("timed out")
	}
}

// Session is an authenticated, encrypted link to one phone.
type Session struct {
	DeviceID string

	conn wire.LineConn
	seal *dlcrypto.Sealer
	open *dlcrypto.Opener
	wmu  sync.Mutex

	closeOnce sync.Once
	done      chan struct{}
}

func newSession(conn wire.LineConn, sendKey, recvKey []byte) (*Session, error) {
	s, err := dlcrypto.NewSealer(sendKey)
	if err != nil {
		return nil, err
	}
	o, err := dlcrypto.NewOpener(recvKey)
	if err != nil {
		return nil, err
	}
	return &Session{conn: conn, seal: s, open: o, done: make(chan struct{})}, nil
}

// NewClientSession builds the phone side of a session; used by the simulator.
func NewClientSession(conn wire.LineConn, keys *dlcrypto.SessionKeys) (*Session, error) {
	return newSession(conn, keys.Up, keys.Down)
}

func (s *Session) Transport() string     { return s.conn.Transport() }
func (s *Session) RemoteAddr() string    { return s.conn.RemoteAddr() }
func (s *Session) Done() <-chan struct{} { return s.done }

type envelope struct {
	Seq  uint64 `json:"seq"`
	Blob string `json:"blob"`
	More bool   `json:"more,omitempty"`
}

// Send encrypts one message, splitting the blob when it exceeds 512 KiB.
func (s *Session) Send(msg any) error {
	pt, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	seq, blob := s.seal.Seal(pt)
	for len(blob) > maxBlob {
		line, _ := json.Marshal(envelope{Seq: seq, Blob: blob[:maxBlob], More: true})
		if err := s.conn.WriteLine(line); err != nil {
			return err
		}
		blob = blob[maxBlob:]
	}
	line, _ := json.Marshal(envelope{Seq: seq, Blob: blob})
	return s.conn.WriteLine(line)
}

// Recv returns the next decrypted message. Numbers stay json.Number so they
// round-trip to clients unchanged. Only one goroutine may call Recv.
func (s *Session) Recv() (map[string]any, error) {
	var parts strings.Builder
	var partSeq uint64
	for {
		line, err := s.conn.ReadLine()
		if err != nil {
			return nil, err
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return nil, fmt.Errorf("bad envelope: %w", err)
		}
		if parts.Len() > 0 && env.Seq != partSeq {
			return nil, errors.New("envelope parts interleaved")
		}
		if env.More {
			if parts.Len()+len(env.Blob) > 64<<20 {
				return nil, errors.New("message exceeds 64 MiB")
			}
			partSeq = env.Seq
			parts.WriteString(env.Blob)
			continue
		}
		blob := env.Blob
		if parts.Len() > 0 {
			parts.WriteString(env.Blob)
			blob = parts.String()
			parts.Reset()
		}
		pt, err := s.open.Open(env.Seq, blob)
		if err != nil {
			return nil, err
		}
		return Decode(pt)
	}
}

func (s *Session) recvWithin(d time.Duration) (map[string]any, error) {
	type res struct {
		m   map[string]any
		err error
	}
	ch := make(chan res, 1)
	go func() {
		m, err := s.Recv()
		ch <- res{m, err}
	}()
	select {
	case r := <-ch:
		return r.m, r.err
	case <-time.After(d):
		s.Close()
		return nil, errors.New("timed out")
	}
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.conn.Close()
	})
}

// Decode parses one JSON object keeping numbers as json.Number.
func Decode(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func Str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func Uint(m map[string]any, k string) uint64 {
	switch v := m[k].(type) {
	case json.Number:
		n, err := v.Int64()
		if err == nil && n > 0 {
			return uint64(n)
		}
	case float64:
		if v > 0 {
			return uint64(v)
		}
	case int64:
		if v > 0 {
			return uint64(v)
		}
	}
	return 0
}
