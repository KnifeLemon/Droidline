package server

import (
	"encoding/json"
	"log/slog"
	"net"

	"github.com/KnifeLemon/Droidline/server/internal/hub"
)

// serveDiscovery answers phone broadcasts on UDP (PROTOCOL.md 4.1).
func serveDiscovery(pc net.PacketConn, h *hub.Hub, agentPort int, log *slog.Logger) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		var q struct {
			Droidline string `json:"droidline"`
			V         int    `json:"v"`
			Server    string `json:"server"`
		}
		if json.Unmarshal(buf[:n], &q) != nil || q.Droidline != "discover" {
			continue
		}
		pairing := h.CodePairingOpen()
		if q.Server != "" && q.Server != h.ServerID() {
			continue
		}
		if q.Server == "" && !pairing {
			continue
		}
		reply, _ := json.Marshal(map[string]any{
			"droidline": "here", "v": 1, "server": h.ServerID(), "name": h.Name(),
			"port": agentPort, "pub": h.PublicKey(), "pairing": pairing,
		})
		pc.WriteTo(reply, addr)
		log.Debug("discovery reply", "to", addr.String())
	}
}
