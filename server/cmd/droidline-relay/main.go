// Command droidline-relay is the self-hosted relay for a VPS or Docker. It does
// what relay/worker does on Cloudflare: pairs a PC and its phones by ID and
// forwards encrypted lines it cannot read.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/KnifeLemon/Droidline/server/internal/relay"
)

func main() {
	listen := flag.String("listen", ":8443", "address to listen on")
	domain := flag.String("domain", "", "get a Let's Encrypt certificate for this domain (needs ports 80 and 443)")
	cert := flag.String("cert", "", "TLS certificate file, when TLS is not terminated by a proxy in front")
	key := flag.String("key", "", "TLS key file")
	cache := flag.String("cache", "certs", "folder for automatic certificates")
	flag.Parse()

	token := os.Getenv("RELAY_TOKEN")
	if len(token) < 16 {
		fmt.Fprintln(os.Stderr, "set RELAY_TOKEN to a random secret of at least 16 characters, for example: openssl rand -hex 24")
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := &http.Server{Addr: *listen, Handler: relay.New(token, log).Handler(), ReadHeaderTimeout: 10 * time.Second}

	var err error
	switch {
	case *domain != "":
		m := &autocert.Manager{Prompt: autocert.AcceptTOS, HostPolicy: autocert.HostWhitelist(*domain), Cache: autocert.DirCache(*cache)}
		go http.ListenAndServe(":80", m.HTTPHandler(nil))
		srv.Addr = ":443"
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate, MinVersion: tls.VersionTLS12}
		log.Info("relay listening", "addr", srv.Addr, "domain", *domain)
		err = srv.ListenAndServeTLS("", "")
	case *cert != "":
		log.Info("relay listening", "addr", srv.Addr, "tls", true)
		err = srv.ListenAndServeTLS(*cert, *key)
	default:
		log.Info("relay listening without TLS; put a TLS proxy in front", "addr", srv.Addr)
		err = srv.ListenAndServe()
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
