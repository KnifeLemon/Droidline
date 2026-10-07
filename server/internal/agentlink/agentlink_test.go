package agentlink

import (
	"strings"
	"testing"

	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
	"github.com/KnifeLemon/Droidline/server/internal/wire"
)

func TestLargeMessageIsSplitAndJoined(t *testing.T) {
	a, b := wire.NewPipe("a", "test")
	keys := &dlcrypto.SessionKeys{Up: make([]byte, 32), Down: make([]byte, 32)}
	keys.Down[0] = 1
	phone, _ := NewClientSession(a, keys)
	srv, _ := newSession(b, keys.Down, keys.Up)

	big := strings.Repeat("x", 1600<<10)
	go func() {
		phone.Send(map[string]any{"event": "big", "data": big})
		phone.Send(map[string]any{"event": "after"})
	}()

	var parts int
	for {
		m, err := srv.Recv()
		if err != nil {
			t.Fatal(err)
		}
		parts++
		if Str(m, "event") == "big" {
			if Str(m, "data") != big {
				t.Fatal("joined payload differs")
			}
			continue
		}
		if Str(m, "event") != "after" || parts != 2 {
			t.Fatalf("seq did not advance correctly: %v", m)
		}
		return
	}
}
