package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/store"
)

// loadTLS returns the self-signed certificate phones pin by SHA-256 for the
// direct route. The key lives in the secret store, the certificate beside it.
func loadTLS(s *store.Store) (*tls.Config, string, error) {
	certPath := filepath.Join(s.Dir, "tls.crt")
	keyPEM := s.Secret("tls_key")
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) || keyPEM == "" {
		certPEM, keyPEM, err = newCert(s.Config.Name)
		if err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
			return nil, "", err
		}
		if err := s.SetSecret("tls_key", keyPEM); err != nil {
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	}
	pair, err := tls.X509KeyPair(certPEM, []byte(keyPEM))
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(pair.Certificate[0])
	cfg := &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	return cfg, hex.EncodeToString(sum[:]), nil
}

func newCert(name string) ([]byte, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "droidline " + name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, "", err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	return certPEM, string(keyPEM), nil
}
