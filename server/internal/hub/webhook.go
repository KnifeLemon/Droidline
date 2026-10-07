package hub

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
)

// webhookSender posts matching notifications to the URLs in config.toml.
// Each request is signed: X-Droidline-Signature = sha256=hex(HMAC(secret, timestamp + "." + body)).
type webhookSender struct {
	hub    *Hub
	client *http.Client
}

func newWebhookSender(h *Hub) *webhookSender {
	return &webhookSender{hub: h, client: &http.Client{Timeout: 10 * time.Second}}
}

// Secret returns the signing secret, creating it on first use.
func (h *Hub) WebhookSecret() string {
	s := h.store.Secret("webhook_secret")
	if s == "" {
		s = "whsec_" + dlcrypto.RandomToken()
		h.store.SetSecret("webhook_secret", s)
	}
	return s
}

func (w *webhookSender) deliver(d *Device, n map[string]any) {
	hooks := w.hub.store.Config.Webhooks
	if len(hooks) == 0 {
		return
	}
	title, _ := n["title"].(string)
	text, _ := n["text"].(string)
	pkg, _ := n["package"].(string)
	payload := map[string]any{"event": "notification", "device_name": d.Name()}
	for k, v := range n {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	secret := w.hub.WebhookSecret()
	for _, hk := range hooks {
		if hk.Device != "" && hk.Device != d.ID && hk.Device != d.Name() {
			continue
		}
		if hk.Package != "" && hk.Package != pkg {
			continue
		}
		if hk.TextContains != "" && !strings.Contains(title, hk.TextContains) && !strings.Contains(text, hk.TextContains) {
			continue
		}
		go w.post(hk.URL, body, secret)
	}
}

func (w *webhookSender) post(url string, body []byte, secret string) {
	ts := fmt.Sprint(time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	backoff := time.Second
	for attempt := 1; attempt <= 3; attempt++ {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "droidline/"+Version)
		req.Header.Set("X-Droidline-Timestamp", ts)
		req.Header.Set("X-Droidline-Signature", sig)
		resp, err := w.client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				if resp.StatusCode >= 300 {
					w.hub.log.Warn("webhook rejected", "url", url, "status", resp.StatusCode)
				}
				return
			}
		}
		if attempt == 3 {
			w.hub.log.Warn("webhook failed", "url", url, "err", err)
			return
		}
		time.Sleep(backoff)
		backoff *= 4
	}
}
