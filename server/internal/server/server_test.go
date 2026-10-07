package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/client"
	"github.com/KnifeLemon/Droidline/server/internal/fakeagent"
	"github.com/KnifeLemon/Droidline/server/internal/store"
)

type rig struct {
	t     *testing.T
	srv   *Server
	cli   *client.Client
	phone *fakeagent.Agent
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.Config.Agent.Listen = "127.0.0.1:0"
	st.Config.Client.Listen = "127.0.0.1:0"
	st.Config.Discovery.Listen = "off"
	st.Config.Lang = "en"
	st.Config.OfflineWait = 5
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := Start(st, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &rig{t: t, srv: srv, cli: mustDial(t, srv)}
}

func mustDial(t *testing.T, srv *Server) *client.Client {
	cli, err := client.Dial(fmt.Sprintf("127.0.0.1:%d", srv.ClientPort), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return cli
}

func (r *rig) call(req map[string]any) map[string]any {
	r.t.Helper()
	res, err := r.cli.Call(req)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

func (r *rig) mustOK(req map[string]any) map[string]any {
	r.t.Helper()
	res := r.call(req)
	if ok, _ := res["ok"].(bool); !ok {
		r.t.Fatalf("%v failed: %v", req["cmd"], res)
	}
	return res
}

// pairByCode runs the 6-digit flow: the phone shows a code, the operator types it.
func (r *rig) pairByCode() {
	phone, err := fakeagent.New("", "Pixel 8", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		r.t.Fatal(err)
	}
	codes := make(chan string, 1)
	phone.OnSAS = func(c string) { codes <- c }
	r.phone = phone
	addr := fmt.Sprintf("127.0.0.1:%d", r.srv.AgentPort)
	go func() {
		if err := phone.PairCode(addr, r.srv.Hub.PublicKey()); err == nil || phone.Paired() != nil {
			phone.Run()
		}
	}()
	r.t.Cleanup(phone.Stop)
	var code string
	select {
	case code = <-codes:
	case <-time.After(5 * time.Second):
		r.t.Fatal("phone never showed a pairing code")
	}
	if res := r.call(map[string]any{"cmd": "pair", "code": "000000"}); res["error"] != "PAIRING_FAILED" {
		r.t.Fatalf("wrong code accepted: %v", res)
	}
	res := r.mustOK(map[string]any{"cmd": "pair", "code": code, "name": "shelf-01"})
	if v, _ := res["value"].(map[string]any); v["name"] != "shelf-01" {
		r.t.Fatalf("pair result %v", res)
	}
}

func TestEndToEnd(t *testing.T) {
	r := newRig(t)

	res := r.call(map[string]any{"cmd": "touch", "by": "text", "value": "x"})
	if res["error"] != "DEVICE_NOT_FOUND" || !strings.Contains(res["msg"].(string), "droidline pair") {
		t.Fatalf("no devices: %v", res)
	}

	r.pairByCode()

	devs := r.mustOK(map[string]any{"cmd": "devices"})["value"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["online"] != true || devs[0].(map[string]any)["route"] != "lan" {
		t.Fatalf("devices %v", devs)
	}

	r.mustOK(map[string]any{"cmd": "launch", "package": "dev.droidline.demo"})
	if v := r.mustOK(map[string]any{"cmd": "exists", "by": "text", "value": "Close ad"})["value"]; v != true {
		t.Fatalf("exists = %v", v)
	}
	if v := r.mustOK(map[string]any{"cmd": "enabled", "args": []any{"id", "login"}})["value"]; v != false {
		t.Fatalf("login button should be disabled before typing, got %v", v)
	}
	r.mustOK(map[string]any{"cmd": "input", "by": "id", "value": "dev.droidline.demo:id/email", "text": "knife"})
	r.mustOK(map[string]any{"cmd": "touchById", "value": "auto_login"})
	if v := r.mustOK(map[string]any{"cmd": "checked", "by": "id", "value": "auto_login"})["value"]; v != true {
		t.Fatalf("checked = %v", v)
	}
	if v := r.mustOK(map[string]any{"cmd": "touch", "device": "shelf-01", "by": "id", "value": "login"})["via"]; v != "node" {
		t.Fatalf("via = %v", v)
	}
	if v := r.mustOK(map[string]any{"cmd": "get_text", "by": "id", "value": "greeting"})["value"]; v != "Welcome, knife" {
		t.Fatalf("greeting %v", v)
	}
	if v := r.mustOK(map[string]any{"cmd": "which", "candidates": []any{[]any{"text", "Log in"}, []any{"id", "main_tab"}}})["value"]; v != float64(1) {
		t.Fatalf("which = %v", v)
	}

	nf := r.call(map[string]any{"cmd": "touch", "by": "text", "value": "Nope", "timeout": 0.2})
	if nf["error"] != "NOT_FOUND" || nf["retryable"] != true ||
		nf["msg"] != "Could not find text 'Nope' within 0.2s. Current screen: dev.droidline.demo / .MainActivity" {
		t.Fatalf("not found: %v", nf)
	}
	if bad := r.call(map[string]any{"cmd": "tuch"}); bad["error"] != "UNKNOWN_CMD" || !strings.Contains(bad["msg"].(string), "Did you mean: touch") {
		t.Fatalf("unknown: %v", bad)
	}
	if bad := r.call(map[string]any{"cmd": "tap", "x": 1}); bad["error"] != "BAD_ARGS" {
		t.Fatalf("bad args: %v", bad)
	}

	shot := r.mustOK(map[string]any{"cmd": "screenshot", "format": "png", "scale": 0.25, "path": "ignored.png"})
	if shot["width"] != float64(270) || len(shot["data"].(string)) < 100 {
		t.Fatalf("screenshot %v %v", shot["width"], len(shot["data"].(string)))
	}
	dump := r.mustOK(map[string]any{"cmd": "dump"})
	if dump["activity"] != ".MainActivity" || dump["tree"] == nil {
		t.Fatalf("dump %v", dump["activity"])
	}

	// Pipelined commands on one connection must run in the order they were sent.
	type out struct {
		i   int
		res map[string]any
	}
	results := make(chan out, 3)
	for i, req := range []map[string]any{
		{"cmd": "clipboard", "text": "one"}, {"cmd": "clipboard", "text": "two"}, {"cmd": "clipboard"},
	} {
		go func(i int, req map[string]any) { results <- out{i, r.call(req)} }(i, req)
		time.Sleep(5 * time.Millisecond)
	}
	for range 3 {
		o := <-results
		if o.i == 2 && o.res["value"] != "two" {
			t.Fatalf("pipelined order broken: read %v", o.res["value"])
		}
	}
}

func TestNetworkCutResultAndResume(t *testing.T) {
	r := newRig(t)
	r.pairByCode()

	// Without wait: accepted now, the result arrives as an event after reconnect.
	res := r.mustOK(map[string]any{"cmd": "airplane", "on": true})
	if res["accepted"] != true {
		t.Fatalf("expected accepted, got %v", res)
	}
	select {
	case ev := <-r.cli.Events:
		if ev["event"] != "result" || ev["ok"] != true || ev["via"] != "settings_macro" {
			t.Fatalf("result event %v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no result event after reconnect")
	}

	// With wait: one response carrying the final result.
	res = r.mustOK(map[string]any{"cmd": "batch", "wait": true, "steps": []any{
		[]any{"airplane", true}, []any{"sleep", 100}, []any{"airplane", false},
	}})
	steps, _ := res["results"].([]any)
	if res["accepted"] == true || len(steps) != 3 {
		t.Fatalf("waited batch %v", res)
	}

	// Commands sent while the phone is offline wait for it to come back.
	r.phone.GoOffline(800 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	r.mustOK(map[string]any{"cmd": "home"})
	if time.Since(start) < 300*time.Millisecond {
		t.Fatal("command should have waited for the phone to reconnect")
	}
}

func TestAgentRestartFailsInFlight(t *testing.T) {
	r := newRig(t)
	r.pairByCode()
	r.mustOK(map[string]any{"cmd": "launch", "package": "dev.droidline.demo"})
	// A batch with a sleep keeps the command in flight while the process "dies".
	done := make(chan map[string]any, 1)
	go func() {
		done <- r.call(map[string]any{"cmd": "batch", "steps": []any{[]any{"sleep", 1500}}})
	}()
	time.Sleep(200 * time.Millisecond)
	r.phone.Restart()
	select {
	case res := <-done:
		if res["error"] != "AGENT_RESTARTED" {
			t.Fatalf("expected AGENT_RESTARTED, got %v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight command never failed")
	}
}

func TestNotifications(t *testing.T) {
	r := newRig(t)
	r.pairByCode()
	r.mustOK(map[string]any{"cmd": "subscribe", "events": []any{"notification"}})
	r.mustOK(map[string]any{"cmd": "notify_filter", "packages": []any{"com.kakao.talk"}})

	waited := make(chan map[string]any, 1)
	go func() {
		waited <- r.call(map[string]any{"cmd": "wait_notification", "by": "textContains", "value": "code", "timeout": 5})
	}()
	time.Sleep(200 * time.Millisecond)
	r.phone.PostNotification("com.other.app", "ignored", "your code 1234")
	r.phone.PostNotification("com.kakao.talk", "Bank", "your code 4821")

	select {
	case ev := <-r.cli.Events:
		if ev["event"] != "notification" || ev["package"] != "com.kakao.talk" {
			t.Fatalf("event %v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification event")
	}
	res := <-waited
	if v, _ := res["value"].(map[string]any); v["text"] != "your code 4821" {
		t.Fatalf("wait_notification %v", res)
	}
	if v := r.mustOK(map[string]any{"cmd": "has_notification", "by": "package", "value": "com.other.app"})["value"]; v != true {
		t.Fatalf("has_notification %v", v)
	}
}

func TestPairByQR(t *testing.T) {
	r := newRig(t)
	uri := r.mustOK(map[string]any{"cmd": "pair_qr", "name": "qr-phone"})["uri"].(string)
	phone, _ := fakeagent.New("", "Galaxy", slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(phone.Stop)
	go phone.PairQR(uri)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		devs := r.mustOK(map[string]any{"cmd": "devices"})["value"].([]any)
		if len(devs) == 1 && devs[0].(map[string]any)["online"] == true {
			if devs[0].(map[string]any)["name"] != "qr-phone" {
				t.Fatalf("name %v", devs[0])
			}
			// The token is single use.
			other, _ := fakeagent.New("", "Thief", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err := other.PairQR(uri); err == nil || !strings.Contains(err.Error(), "pairing_closed") {
				t.Fatalf("reused QR token: %v", err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("QR pairing did not complete")
}

func TestHTTPGuardAndAPI(t *testing.T) {
	r := newRig(t)
	r.pairByCode()
	base := fmt.Sprintf("http://127.0.0.1:%d", r.srv.ClientPort)

	post := func(path, body string, hdr map[string]string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", base+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}

	code, m := post("/devices/_/launch", `{"package":"dev.droidline.demo"}`, nil)
	if code != 200 || m["ok"] != true {
		t.Fatalf("launch %d %v", code, m)
	}
	code, m = post("/devices/shelf-01/touch", `{"by":"text","value":"Missing","timeout":0.1}`, nil)
	if code != 422 || m["error"] != "NOT_FOUND" {
		t.Fatalf("not found over http %d %v", code, m)
	}
	if code, _ = post("/devices/_/home", `{}`, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("browser origin should be blocked, got %d", code)
	}
	if code, _ = post("/devices/_/home", `{}`, map[string]string{"Host": "attacker.example:8780"}); code != 403 {
		t.Fatalf("rebinding host should be blocked, got %d", code)
	}
	req, _ := http.NewRequest("POST", base+"/devices/_/home", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 415 {
		t.Fatalf("text/plain POST should be refused, got %d", resp.StatusCode)
	}

	resp, err := http.Get(base + "/devices/_/screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" || !bytes.HasPrefix(img, []byte("\x89PNG")) {
		t.Fatalf("screenshot.png: %s %d bytes", resp.Header.Get("Content-Type"), len(img))
	}
}

// A phone coming back from a dead mobile link opens a new connection while the
// server still holds the old one; the new session must replace it cleanly.
func TestReconnectWhileOldLinkLooksAlive(t *testing.T) {
	r := newRig(t)
	r.pairByCode()
	if err := r.phone.DuplicateLink(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 3; i++ {
		res := r.call(map[string]any{"cmd": "info", "offline_wait": 5})
		if ok, _ := res["ok"].(bool); !ok {
			t.Fatalf("after replacement: %v", res)
		}
	}
	devs := r.mustOK(map[string]any{"cmd": "devices"})["value"].([]any)
	if devs[0].(map[string]any)["online"] != true {
		t.Fatal("device should be online")
	}
}
