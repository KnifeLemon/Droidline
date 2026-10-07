package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/KnifeLemon/Droidline/server/internal/wire"
)

var errEOF = io.EOF

func decodeB64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

type relayFrame struct {
	Device string `json:"device"`
	Line   string `json:"line,omitempty"`
	Event  string `json:"event,omitempty"`
	Close  bool   `json:"close,omitempty"`
}

// runRelay keeps one outbound WebSocket to the relay and turns each phone on it
// into an in-memory link handed to the hub, exactly like a direct connection.
func runRelay(ctx context.Context, s *Server) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := relayOnce(ctx, s)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		s.Log.Warn("relay disconnected; retrying", "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func relayOnce(ctx context.Context, s *Server) error {
	token := s.Store.Secret("relay_token")
	u, err := url.Parse(strings.TrimRight(s.Store.Config.Relay.URL, "/") + "/v1/server")
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	q := u.Query()
	q.Set("server", s.Hub.ServerID())
	u.RawQuery = q.Encode()
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, u.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		return err
	}
	c.SetReadLimit(wire.MaxLine + 4096)
	defer c.CloseNow()
	s.Log.Info("relay connected", "url", s.Store.Config.Relay.URL)

	var wmu sync.Mutex
	send := func(f relayFrame) error {
		b, _ := json.Marshal(f)
		wmu.Lock()
		defer wmu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, b)
	}
	phones := map[string]*wire.Pipe{}
	defer func() {
		for _, p := range phones {
			p.Close()
		}
	}()
	open := func(device string) *wire.Pipe {
		if old := phones[device]; old != nil {
			old.Close()
		}
		hubSide, relaySide := wire.NewPipe("relay:"+device, "relay")
		phones[device] = relaySide
		go s.Hub.AcceptAgent(hubSide)
		go func() {
			defer relaySide.Close()
			for {
				line, err := relaySide.ReadLine()
				if err != nil {
					send(relayFrame{Device: device, Close: true})
					return
				}
				if send(relayFrame{Device: device, Line: string(line)}) != nil {
					return
				}
			}
		}()
		return relaySide
	}

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		var f relayFrame
		if json.Unmarshal(data, &f) != nil || f.Device == "" {
			continue
		}
		switch {
		case f.Event == "open":
			open(f.Device)
		case f.Event == "close":
			if p := phones[f.Device]; p != nil {
				p.Close()
				delete(phones, f.Device)
			}
		case f.Line != "":
			p := phones[f.Device]
			if p == nil {
				p = open(f.Device)
			}
			p.WriteLine([]byte(f.Line))
		}
	}
}
