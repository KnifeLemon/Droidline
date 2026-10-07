# Relay

Use a relay when the PC and the phones are both behind NAT, for example phones on LTE or 5G and a PC on a home router. Both sides connect out to the relay, which pairs them by ID and forwards lines it cannot read. If the phones share the PC's Wi-Fi, or you can forward one port to the PC, you do not need one.

Nobody runs a public relay for Droidline. Deploy your own:

* [`worker/`](worker): Cloudflare Workers. No server to keep running; a few phones fit in the free plan.
* `droidline-relay`: one binary or a Docker image for a VPS.

## droidline-relay

```bash
export RELAY_TOKEN=$(openssl rand -hex 24)
droidline-relay --domain relay.example.com      # Let's Encrypt on ports 80 and 443
droidline-relay --listen :8443                  # behind your own TLS proxy
```

Docker:

```bash
docker run -d --restart unless-stopped -e RELAY_TOKEN=... -p 8443:8443 ghcr.io/knifelemon/droidline-relay
```

Then on the PC:

```bash
droidline relay set https://relay.example.com <RELAY_TOKEN>
```

Restart `droidline serve`. Paired phones learn the relay on their next connection; new phones get it from the pairing QR.
