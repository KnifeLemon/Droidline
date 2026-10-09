package hub

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// Leases are optional. A phone nobody leased behaves as before; while leased, only
// requests that carry the lease reach it.
type leaseTable struct {
	mu       sync.Mutex
	byDevice map[string]*lease
	byID     map[string]*lease
}

type lease struct {
	id, device string
	ttl        time.Duration
	last       time.Time
	busy       int
}

func (l *lease) expired(now time.Time) bool { return l.busy == 0 && now.Sub(l.last) > l.ttl }

func (t *leaseTable) init() {
	if t.byDevice == nil {
		t.byDevice = map[string]*lease{}
		t.byID = map[string]*lease{}
	}
}

// dropExpiredLocked frees phones whose lease sat idle past its ttl.
func (t *leaseTable) dropExpiredLocked(now time.Time) {
	for dev, l := range t.byDevice {
		if l.expired(now) {
			delete(t.byDevice, dev)
			delete(t.byID, l.id)
		}
	}
}

func (h *Hub) isLeased(deviceID string) bool {
	t := &h.leases
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	t.dropExpiredLocked(time.Now())
	return t.byDevice[deviceID] != nil
}

// leaseDevice resolves the device a lease belongs to, for requests that name only the lease.
func (h *Hub) leaseDevice(id string) (string, bool) {
	t := &h.leases
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	t.dropExpiredLocked(time.Now())
	l := t.byID[id]
	if l == nil {
		return "", false
	}
	return l.device, true
}

// admit checks a request against the phone's lease. On success it returns a
// function to call when the request finishes, so a long command keeps the lease alive.
func (h *Hub) admit(d *Device, leaseID string) (func(), Result) {
	t := &h.leases
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	now := time.Now()
	t.dropExpiredLocked(now)
	l := t.byDevice[d.ID]
	if leaseID != "" && (l == nil || l.id != leaseID) {
		if t.byID[leaseID] == nil {
			return nil, h.errResult("LEASE_NOT_FOUND", map[string]any{"lease": leaseID})
		}
	}
	if l == nil {
		return func() {}, nil
	}
	if l.id != leaseID {
		return nil, h.errResult("DEVICE_LEASED", map[string]any{"device": d.Name()})
	}
	l.busy++
	l.last = now
	return func() {
		t.mu.Lock()
		l.busy--
		l.last = time.Now()
		t.mu.Unlock()
	}, nil
}

func (h *Hub) leaseCmd(p map[string]any) Result {
	wait := 30.0
	if v, ok := p["wait"].(float64); ok {
		wait = v
	}
	ttl := 300.0
	if v, ok := p["ttl"].(float64); ok {
		ttl = v
	}
	ttl = min(max(ttl, 10), 86400)
	ref, _ := p["device"].(string)
	model, _ := p["model"].(string)
	var minSDK int64
	if v, ok := p["min_sdk"].(int64); ok {
		minSDK = v
	}
	if ref != "" && h.deviceByRef(ref) == nil {
		return h.errResult("DEVICE_NOT_FOUND", map[string]any{"device": ref})
	}

	deadline := time.Now().Add(time.Duration(wait * float64(time.Second)))
	for {
		if r := h.tryLease(ref, model, minSDK, time.Duration(ttl*float64(time.Second))); r != nil {
			return r
		}
		if time.Now().After(deadline) {
			return h.errResult("NO_FREE_DEVICE", map[string]any{"wait": wait})
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (h *Hub) tryLease(ref, model string, minSDK int64, ttl time.Duration) Result {
	// Read the phones first: Device.Info takes the lease lock itself.
	var cands []*Device
	for _, d := range h.sortedDevices() {
		if ref != "" && ref != d.ID && ref != d.Name() {
			continue
		}
		if !d.Online() || int64(d.sdk()) < minSDK || (model != "" && !strings.Contains(d.model(), model)) {
			continue
		}
		cands = append(cands, d)
	}
	t := &h.leases
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	now := time.Now()
	t.dropExpiredLocked(now)
	for _, d := range cands {
		if t.byDevice[d.ID] != nil {
			continue
		}
		l := &lease{id: newLeaseID(), device: d.ID, ttl: ttl, last: now}
		t.byDevice[d.ID] = l
		t.byID[l.id] = l
		h.log.Info("leased", "device", d.Name(), "lease", l.id)
		return Result{"ok": true, "lease": l.id, "device": d.ID, "name": d.Name(), "ttl": ttl.Seconds()}
	}
	return nil
}

func (h *Hub) releaseLease(id string) {
	t := &h.leases
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	if l := t.byID[id]; l != nil {
		delete(t.byID, id)
		delete(t.byDevice, l.device)
		h.log.Info("released", "lease", id)
	}
}

func (h *Hub) releaseDevice(deviceID string) {
	t := &h.leases
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	if l := t.byDevice[deviceID]; l != nil {
		delete(t.byID, l.id)
		delete(t.byDevice, deviceID)
		h.log.Info("released by device", "device", deviceID, "lease", l.id)
	}
}

func newLeaseID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "l-" + hex.EncodeToString(b)
}
