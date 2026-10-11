package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientDialAddr(t *testing.T) {
	cases := []struct {
		config, want string
		ok           bool
	}{
		{"[client]\nlisten = \"127.0.0.1:11225\"\n", "127.0.0.1:11225", true},
		{"[client]\nlisten = \":11225\"\n", "127.0.0.1:11225", true},
		{"[client]\nlisten = \"0.0.0.0:11225\"\n", "127.0.0.1:11225", true},
		{"[client]\nlisten = \"[::]:11225\"\n", "127.0.0.1:11225", true},
		{"[client]\nlisten = \"192.168.0.10:11225\"\n", "192.168.0.10:11225", true},
		{"[agent]\nlisten = \":11223\"\n", "", false},
		{"not = [valid", "", false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.config), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok := ClientDialAddr(dir)
		if got != c.want || ok != c.ok {
			t.Errorf("config %q: got %q %v, want %q %v", c.config, got, ok, c.want, c.ok)
		}
	}
}

func TestClientDialAddrWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ClientDialAddr(dir); ok {
		t.Fatal("want ok false without config.toml")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("ClientDialAddr must not create config.toml")
	}
}
