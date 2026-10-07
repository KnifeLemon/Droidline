// Package store keeps the server's files: config.toml (edited by people),
// state.json (paired devices, written by the server) and the secret store.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Name        string  `toml:"name"`
	Lang        string  `toml:"lang"`
	OfflineWait float64 `toml:"offline_wait"`
	Agent       struct {
		Listen string `toml:"listen"`
	} `toml:"agent"`
	Client struct {
		Listen         string   `toml:"listen"`
		RequireToken   bool     `toml:"require_token"`
		AllowedOrigins []string `toml:"allowed_origins"`
	} `toml:"client"`
	Discovery struct {
		Listen string `toml:"listen"`
	} `toml:"discovery"`
	Pairing struct {
		Code bool `toml:"code"`
	} `toml:"pairing"`
	Remote struct {
		Addresses []string `toml:"addresses"`
	} `toml:"remote"`
	Relay struct {
		URL string `toml:"url"`
	} `toml:"relay"`
	Webhooks []Webhook `toml:"webhooks"`
}

type Webhook struct {
	URL          string `toml:"url"`
	Device       string `toml:"device"`
	Package      string `toml:"package"`
	TextContains string `toml:"text_contains"`
}

const defaultConfig = `# Droidline server settings. Restart "droidline serve" after editing.

# Name phones show for this PC. Empty means the computer name.
name = ""
# Language for error messages: en, ko or zh. Empty means the system language.
lang = ""
# Seconds a command waits for an offline phone to come back before DEVICE_OFFLINE.
offline_wait = 30

[agent]
# Phones connect here. One forwarded port is enough for every remote route.
listen = ":8779"

[client]
# SDKs, the CLI, MCP and curl connect here. Keep it on 127.0.0.1 unless you need
# other machines to reach it; then a client token is always required.
listen = "127.0.0.1:8780"
require_token = false
# Web pages allowed to call the local HTTP API. Leave empty to block all browsers.
allowed_origins = []

[discovery]
listen = ":8778"

[pairing]
# Allow phones on the same Wi-Fi to pair by showing a 6-digit code.
code = true

[remote]
# Extra addresses given to phones in the pairing QR, tried after the same Wi-Fi.
# Examples: "tls://203.0.113.5:8779", "wss://droid.example.com/agent"
addresses = []

[relay]
# Set with: droidline relay set <url> <token>
url = ""

# Forward matching notifications as signed POST requests.
# [[webhooks]]
# url = "https://example.com/hooks/droidline"
# package = "com.kakao.talk"
# text_contains = ""

# Named upstream proxies live in the secret store, not here:
#   droidline proxy add kr1 socks5://user:pass@1.2.3.4:1080
# then use proxy("@kr1", app="com.android.chrome").
`

type DeviceRecord struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Pub          string `json:"pub"`
	Model        string `json:"model,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	SDK          int    `json:"sdk,omitempty"`
	Release      string `json:"release,omitempty"`
	Agent        string `json:"agent,omitempty"`
	PairedAt     int64  `json:"paired_at"`
	LastSeen     int64  `json:"last_seen,omitempty"`
}

type TokenRecord struct {
	ID      string `json:"id"`
	Hash    string `json:"hash"`
	Label   string `json:"label,omitempty"`
	Created int64  `json:"created"`
}

type state struct {
	ServerID     string         `json:"server_id"`
	Devices      []DeviceRecord `json:"devices"`
	ClientTokens []TokenRecord  `json:"client_tokens"`
}

type Store struct {
	Dir    string
	Config Config

	mu      sync.Mutex
	st      state
	secrets map[string]string
	stMod   time.Time
	secMod  time.Time
}

// DefaultDir is %APPDATA%\Droidline on Windows, ~/Library/Application Support/Droidline
// on macOS and ~/.config/droidline elsewhere. DROIDLINE_HOME overrides it.
func DefaultDir() string {
	if d := os.Getenv("DROIDLINE_HOME"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ".droidline"
	}
	if strings.Contains(base, "AppData") || strings.Contains(base, "Application Support") {
		return filepath.Join(base, "Droidline")
	}
	return filepath.Join(base, "droidline")
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{Dir: dir}
	cfgPath := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o600); err != nil {
			return nil, err
		}
	}
	// Keys missing from a hand-written config keep these values.
	s.Config.Pairing.Code = true
	if _, err := toml.DecodeFile(cfgPath, &s.Config); err != nil {
		return nil, fmt.Errorf("%s: %w", cfgPath, err)
	}
	s.applyDefaults()

	s.secrets = map[string]string{}
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// refreshLocked re-reads state.json and the secret store when another process
// (the CLI next to a running server) changed them, so neither overwrites the other.
func (s *Store) refreshLocked() error {
	statePath := filepath.Join(s.Dir, "state.json")
	if fi, err := os.Stat(statePath); err == nil && !fi.ModTime().Equal(s.stMod) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		var st state
		if err := json.Unmarshal(raw, &st); err != nil {
			return fmt.Errorf("state.json: %w", err)
		}
		s.st, s.stMod = st, fi.ModTime()
	}
	secPath := filepath.Join(s.Dir, secretsFile)
	if fi, err := os.Stat(secPath); err == nil && !fi.ModTime().Equal(s.secMod) {
		sec, err := loadSecrets(s.Dir)
		if err != nil {
			return fmt.Errorf("secret store: %w", err)
		}
		s.secrets, s.secMod = sec, fi.ModTime()
	}
	return nil
}

func (s *Store) applyDefaults() {
	c := &s.Config
	if c.Name == "" {
		c.Name, _ = os.Hostname()
	}
	if c.Lang == "" {
		c.Lang = SystemLang()
	}
	if c.OfflineWait == 0 {
		c.OfflineWait = 30
	}
	if c.Agent.Listen == "" {
		c.Agent.Listen = ":8779"
	}
	if c.Client.Listen == "" {
		c.Client.Listen = "127.0.0.1:8780"
	}
	if c.Discovery.Listen == "" {
		c.Discovery.Listen = ":8778"
	}
}

func (s *Store) saveState() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, "state.json")
	if err := writeAtomic(path, data, 0o600); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		s.stMod = fi.ModTime()
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) ServerID(newID func() string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if s.st.ServerID == "" {
		s.st.ServerID = newID()
		if err := s.saveState(); err != nil {
			return "", err
		}
	}
	return s.st.ServerID, nil
}

func (s *Store) Devices() []DeviceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	out := append([]DeviceRecord(nil), s.st.Devices...)
	sort.Slice(out, func(i, j int) bool { return out[i].PairedAt < out[j].PairedAt })
	return out
}

func (s *Store) Device(id string) (DeviceRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	for _, d := range s.st.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return DeviceRecord{}, false
}

// PutDevice inserts or replaces a device record by ID.
func (s *Store) PutDevice(d DeviceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	for i := range s.st.Devices {
		if s.st.Devices[i].ID == d.ID {
			s.st.Devices[i] = d
			return s.saveState()
		}
	}
	s.st.Devices = append(s.st.Devices, d)
	return s.saveState()
}

func (s *Store) RemoveDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	out := s.st.Devices[:0]
	for _, d := range s.st.Devices {
		if d.ID != id {
			out = append(out, d)
		}
	}
	s.st.Devices = out
	return s.saveState()
}

// TouchDevice records last-seen without rewriting the file more than once a minute.
func (s *Store) TouchDevice(id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	for i := range s.st.Devices {
		if s.st.Devices[i].ID == id {
			if now.Unix()-s.st.Devices[i].LastSeen >= 60 {
				s.st.Devices[i].LastSeen = now.Unix()
				s.saveState()
			}
			return
		}
	}
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func (s *Store) AddClientToken(id, token, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	s.st.ClientTokens = append(s.st.ClientTokens, TokenRecord{ID: id, Hash: hashToken(token), Label: label, Created: time.Now().Unix()})
	return s.saveState()
}

func (s *Store) ClientTokens() []TokenRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	return append([]TokenRecord(nil), s.st.ClientTokens...)
}

func (s *Store) RemoveClientToken(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	out := s.st.ClientTokens[:0]
	found := false
	for _, t := range s.st.ClientTokens {
		if t.ID == id {
			found = true
			continue
		}
		out = append(out, t)
	}
	s.st.ClientTokens = out
	return found, s.saveState()
}

// CheckClientToken compares hashes, so the token itself is never stored.
func (s *Store) CheckClientToken(token string) bool {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	for _, t := range s.st.ClientTokens {
		if t.Hash == h {
			return true
		}
	}
	return false
}

func (s *Store) Secret(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	return s.secrets[key]
}

func (s *Store) SetSecret(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if value == "" {
		delete(s.secrets, key)
	} else {
		s.secrets[key] = value
	}
	if err := saveSecrets(s.Dir, s.secrets); err != nil {
		return err
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, secretsFile)); err == nil {
		s.secMod = fi.ModTime()
	}
	return nil
}

// SetRelayURL rewrites relay.url in config.toml in place so comments survive.
func (s *Store) SetRelayURL(url string) error {
	path := filepath.Join(s.Dir, "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	inRelay, done := false, false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			inRelay = t == "[relay]"
			continue
		}
		if inRelay && strings.HasPrefix(t, "url") {
			lines[i] = fmt.Sprintf("url = %q", url)
			done = true
			break
		}
	}
	if !done {
		lines = append(lines, "", "[relay]", fmt.Sprintf("url = %q", url))
	}
	s.Config.Relay.URL = url
	return writeAtomic(path, []byte(strings.Join(lines, "\n")), 0o600)
}

// SecretKeys lists secret names with the given prefix, sorted.
func (s *Store) SecretKeys(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	var out []string
	for k := range s.secrets {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
