package server

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/relay"
	"github.com/KnifeLemon/Droidline/server/internal/store"
)

func TestThroughRelay(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	const token = "relay-token-for-tests-only"
	rl := httptest.NewServer(relay.New(token, quiet).Handler())
	defer rl.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.Config.Agent.Listen = "127.0.0.1:0"
	st.Config.Client.Listen = "127.0.0.1:0"
	st.Config.Discovery.Listen = "off"
	st.Config.Relay.URL = rl.URL
	st.SetSecret("relay_token", token)
	srv, err := Start(st, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	r := &rig{t: t, srv: srv}
	r.cli = mustDial(t, srv)
	r.pairByCode()

	// Leave the LAN: from now on the phone can only reach the PC through the relay.
	r.phone.UseRelay(true)
	deadline := time.Now().Add(10 * time.Second)
	for {
		devs := r.mustOK(map[string]any{"cmd": "devices"})["value"].([]any)
		d := devs[0].(map[string]any)
		if d["online"] == true && d["route"] == "relay" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("phone never came back through the relay: %v", d)
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.mustOK(map[string]any{"cmd": "launch", "package": "dev.droidline.demo"})
	r.mustOK(map[string]any{"cmd": "input", "by": "id", "value": "email", "text": "relay"})
	r.mustOK(map[string]any{"cmd": "touch", "by": "id", "value": "login"})
	if v := r.mustOK(map[string]any{"cmd": "get_text", "by": "id", "value": "greeting"})["value"]; v != "Welcome, relay" {
		t.Fatalf("greeting %v", v)
	}
	// A screenshot is larger than one 512 KiB envelope part at full scale.
	shot := r.mustOK(map[string]any{"cmd": "screenshot", "format": "png"})
	if len(shot["data"].(string)) < 1000 {
		t.Fatal("screenshot through relay is empty")
	}
}
