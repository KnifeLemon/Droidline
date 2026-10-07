package dlcrypto

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type vectors struct {
	Keys map[string]struct {
		D   string `json:"d"`
		Pub string `json:"pub"`
	} `json:"keys"`
	Handshakes []struct {
		Mode  string  `json:"mode"`
		Line1 string  `json:"line1"`
		Line2 string  `json:"line2"`
		KUp   string  `json:"k_up"`
		KDn   string  `json:"k_dn"`
		SAS   string  `json:"sas"`
		Token *string `json:"token"`
		Up    struct {
			Plaintext string `json:"plaintext"`
			Envelope  struct {
				Seq  uint64 `json:"seq"`
				Blob string `json:"blob"`
			} `json:"envelope"`
		} `json:"up_seq0"`
		Dn struct {
			Plaintext string `json:"plaintext"`
			Envelope  struct {
				Seq  uint64 `json:"seq"`
				Blob string `json:"blob"`
			} `json:"envelope"`
		} `json:"dn_seq1"`
	} `json:"handshakes"`
	Relay struct {
		Token  string `json:"relay_token"`
		Device struct {
			Server, Device, Ticket string
		} `json:"device_ticket"`
		Enroll struct {
			Server string
			Expiry int64
			Ticket string
		} `json:"enroll_ticket"`
	} `json:"relay"`
}

func loadVectors(t *testing.T) vectors {
	raw, err := os.ReadFile("../../../spec/test-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHandshakeVectors(t *testing.T) {
	v := loadVectors(t)
	key := func(name string) []byte {
		d, _ := hex.DecodeString(v.Keys[name].D)
		return d
	}
	srvStatic, _ := PrivateKey(key("server_static"))
	srvEph, _ := PrivateKey(key("server_eph"))
	phoneStatic, _ := PrivateKey(key("phone_static"))
	phoneEph, _ := PrivateKey(key("phone_eph"))
	if EncodePublic(srvStatic.PublicKey()) != v.Keys["server_static"].Pub {
		t.Fatal("public key encoding differs from vectors")
	}

	for _, hs := range v.Handshakes {
		t.Run(hs.Mode, func(t *testing.T) {
			var token []byte
			if hs.Token != nil {
				token, _ = UnB64(*hs.Token)
			}
			// Server view and phone view must agree with each other and with the vectors.
			srv, err := Derive([]byte(hs.Line1), []byte(hs.Line2), srvEph, phoneEph.PublicKey(), srvStatic, phoneStatic.PublicKey(), token)
			if err != nil {
				t.Fatal(err)
			}
			ph, _ := Derive([]byte(hs.Line1), []byte(hs.Line2), phoneEph, srvEph.PublicKey(), phoneStatic, srvStatic.PublicKey(), token)
			if hex.EncodeToString(srv.Up) != hs.KUp || hex.EncodeToString(ph.Up) != hs.KUp {
				t.Errorf("k_up mismatch")
			}
			if hex.EncodeToString(srv.Down) != hs.KDn {
				t.Errorf("k_dn mismatch")
			}
			if srv.SAS != hs.SAS || ph.SAS != hs.SAS {
				t.Errorf("sas = %s/%s, want %s", srv.SAS, ph.SAS, hs.SAS)
			}

			op, _ := NewOpener(srv.Up)
			pt, err := op.Open(hs.Up.Envelope.Seq, hs.Up.Envelope.Blob)
			if err != nil || string(pt) != hs.Up.Plaintext {
				t.Fatalf("open up envelope: %v %q", err, pt)
			}
			s, _ := NewSealer(srv.Down)
			s.Seal([]byte("skip seq 0"))
			seq, blob := s.Seal([]byte(hs.Dn.Plaintext))
			if seq != hs.Dn.Envelope.Seq || blob != hs.Dn.Envelope.Blob {
				t.Errorf("down envelope differs from vectors")
			}
		})
	}
}

func TestOpenerRejectsReplayAndTamper(t *testing.T) {
	key := make([]byte, 32)
	s, _ := NewSealer(key)
	o, _ := NewOpener(key)
	seq, blob := s.Seal([]byte(`{"a":1}`))
	if _, err := o.Open(seq, blob); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Open(seq, blob); err == nil {
		t.Fatal("replayed envelope accepted")
	}
	seq, blob = s.Seal([]byte(`{"a":2}`))
	tampered := []byte(blob)
	tampered[5] ^= 1
	if _, err := o.Open(seq, string(tampered)); err == nil {
		t.Fatal("tampered envelope accepted")
	}
}

func TestTickets(t *testing.T) {
	v := loadVectors(t)
	if got := DeviceTicket(v.Relay.Token, v.Relay.Device.Server, v.Relay.Device.Device); got != v.Relay.Device.Ticket {
		t.Errorf("device ticket %s, want %s", got, v.Relay.Device.Ticket)
	}
	if got := EnrollTicket(v.Relay.Token, v.Relay.Enroll.Server, v.Relay.Enroll.Expiry); got != v.Relay.Enroll.Ticket {
		t.Errorf("enroll ticket %s, want %s", got, v.Relay.Enroll.Ticket)
	}
}
