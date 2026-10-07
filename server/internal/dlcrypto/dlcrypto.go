// Package dlcrypto implements the key schedule and envelope from PROTOCOL.md
// sections 4.3-4.5, plus relay tickets from section 6.2.
package dlcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var b64 = base64.RawURLEncoding

func B64(b []byte) string { return b64.EncodeToString(b) }

func UnB64(s string) ([]byte, error) { return b64.DecodeString(strings.TrimRight(s, "=")) }

func GenerateKey() (*ecdh.PrivateKey, error) { return ecdh.P256().GenerateKey(rand.Reader) }

// PrivateKey restores a P-256 key from its 32-byte scalar.
func PrivateKey(d []byte) (*ecdh.PrivateKey, error) { return ecdh.P256().NewPrivateKey(d) }

// PublicKey decodes a base64url uncompressed P-256 point.
func PublicKey(s string) (*ecdh.PublicKey, error) {
	raw, err := UnB64(s)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	return ecdh.P256().NewPublicKey(raw)
}

func EncodePublic(k *ecdh.PublicKey) string { return B64(k.Bytes()) }

func Nonce() []byte {
	b := make([]byte, 16)
	rand.Read(b)
	return b
}

type SessionKeys struct {
	Up   []byte // phone to server
	Down []byte // server to phone
	SAS  string
}

// Derive runs the key schedule over the two handshake lines exactly as sent.
// token is nil except in pair_qr mode.
func Derive(line1, line2 []byte, ephPriv *ecdh.PrivateKey, ephPeer *ecdh.PublicKey,
	staticPriv *ecdh.PrivateKey, staticPeer *ecdh.PublicKey, token []byte) (*SessionKeys, error) {
	e, err := ephPriv.ECDH(ephPeer)
	if err != nil {
		return nil, err
	}
	s, err := staticPriv.ECDH(staticPeer)
	if err != nil {
		return nil, err
	}
	th := sha256.New()
	th.Write(line1)
	th.Write([]byte("\n"))
	th.Write(line2)
	ikm := append(append(e, s...), token...)
	prk, err := hkdf.Extract(sha256.New, ikm, th.Sum(nil))
	if err != nil {
		return nil, err
	}
	expand := func(info string, n int) []byte {
		out, _ := hkdf.Expand(sha256.New, prk, info, n)
		return out
	}
	sas := binary.BigEndian.Uint32(expand("droidline v1 sas", 4)) % 1000000
	return &SessionKeys{
		Up:   expand("droidline v1 up", 32),
		Down: expand("droidline v1 down", 32),
		SAS:  fmt.Sprintf("%06d", sas),
	}, nil
}

var aad = []byte("droidline v1")

// Sealer encrypts one direction of a connection. Not safe for concurrent use.
type Sealer struct {
	aead cipher.AEAD
	seq  uint64
}

func NewSealer(key []byte) (*Sealer, error) {
	a, err := newAEAD(key)
	return &Sealer{aead: a}, err
}

// Seal returns the seq used and the base64url ciphertext.
func (s *Sealer) Seal(plaintext []byte) (uint64, string) {
	seq := s.seq
	s.seq++
	return seq, B64(s.aead.Seal(nil, nonce(seq), plaintext, aad))
}

// Opener decrypts one direction and enforces strictly sequential seq numbers.
type Opener struct {
	aead cipher.AEAD
	next uint64
}

func NewOpener(key []byte) (*Opener, error) {
	a, err := newAEAD(key)
	return &Opener{aead: a}, err
}

var ErrSeq = errors.New("envelope seq out of order")

func (o *Opener) Open(seq uint64, blob string) ([]byte, error) {
	if seq != o.next {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSeq, seq, o.next)
	}
	ct, err := UnB64(blob)
	if err != nil {
		return nil, err
	}
	pt, err := o.aead.Open(nil, nonce(seq), ct, aad)
	if err != nil {
		return nil, errors.New("envelope failed to authenticate")
	}
	o.next++
	return pt, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(seq uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

func DeviceTicket(relayToken, server, device string) string {
	return mac(relayToken, "droidline device|"+server+"|"+device)
}

func EnrollTicket(relayToken, server string, expiry int64) string {
	return mac(relayToken, fmt.Sprintf("droidline enroll|%s|%d", server, expiry))
}

func mac(key, msg string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(msg))
	return B64(h.Sum(nil))
}

// TicketEqual compares tickets in constant time.
func TicketEqual(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

// RandomID returns n lowercase base32 characters, the format of device and server IDs.
func RandomID(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// RandomToken returns 16 random bytes, base64url encoded.
func RandomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return B64(b)
}
