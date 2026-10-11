package main

import (
	"os"
	"path/filepath"
	"testing"
)

func homeWithClientPort(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := "[client]\nlisten = \"127.0.0.1:11225\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func clearAddrEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DROIDLINE_ADDR", "DROIDLINE_HOST", "DROIDLINE_PORT"} {
		t.Setenv(k, "")
	}
}

func TestAddrComesFromConfig(t *testing.T) {
	clearAddrEnv(t)
	g, _ := parseGlobals([]string{"--home", homeWithClientPort(t), "pair"})
	if g.addr != "127.0.0.1:11225" {
		t.Fatalf("addr %q, want the [client] listen port", g.addr)
	}
}

func TestAddrFlagWins(t *testing.T) {
	clearAddrEnv(t)
	g, _ := parseGlobals([]string{"--home", homeWithClientPort(t), "--addr", "127.0.0.1:9999", "pair"})
	if g.addr != "127.0.0.1:9999" {
		t.Fatalf("addr %q, want --addr", g.addr)
	}
}

func TestAddrEnvWins(t *testing.T) {
	clearAddrEnv(t)
	t.Setenv("DROIDLINE_ADDR", "127.0.0.1:9998")
	g, _ := parseGlobals([]string{"--home", homeWithClientPort(t), "pair"})
	if g.addr != "127.0.0.1:9998" {
		t.Fatalf("addr %q, want DROIDLINE_ADDR", g.addr)
	}
}

func TestAddrDefaultWithoutConfig(t *testing.T) {
	clearAddrEnv(t)
	g, _ := parseGlobals([]string{"--home", t.TempDir(), "pair"})
	if g.addr != "127.0.0.1:8780" {
		t.Fatalf("addr %q, want the default", g.addr)
	}
}
