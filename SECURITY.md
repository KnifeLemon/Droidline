# Security

Droidline can type, tap and read everything on a phone, so a flaw in it is serious. Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on <https://github.com/KnifeLemon/Droidline>. Do not open a public issue for them.

## What protects you

| Threat | Protection |
|---|---|
| Someone on your Wi-Fi sends commands to your phone | The phone only accepts commands from the PC it paired with. Pairing needs the QR code or a 6-digit code you read off the phone and type on the PC. |
| Someone on your Wi-Fi reads traffic | After the two-line handshake every line is AES-256-GCM encrypted with keys from an ECDH exchange. Ephemeral keys give forward secrecy. |
| A relay or tunnel operator, including Cloudflare, reads traffic | Same end-to-end encryption. The relay sees device IDs, timing and sizes, never commands or screen content. |
| A fake PC on your Wi-Fi answers discovery | The handshake mixes in the PC's static key, which the phone stored at pairing. A fake PC derives different keys and the first encrypted line fails. |
| A web page you visit calls `localhost:8780` | The local HTTP API rejects browser `Origin` headers, non-localhost `Host` headers (DNS rebinding), and non-JSON POSTs. |
| Another machine reaches the client port | The client port listens on 127.0.0.1. If you open it, every connection needs a client token, stored hashed. |
| A leaked pairing QR | The token inside is single-use and expires after 10 minutes. |
| A lost or retired phone | `droidline revoke <device>` deletes its key; it cannot reconnect without pairing again. |

Secrets on the PC (the server key, TLS key, relay token, saved proxy passwords) are kept with DPAPI on Windows and in a file only your user can read elsewhere.

## Design notes

The handshake and envelope are specified in [spec/PROTOCOL.md](spec/PROTOCOL.md), sections 4.3 to 4.5. They use standard primitives (P-256 ECDH, HKDF-SHA256, AES-256-GCM) composed in a scheme close to the Noise KK pattern, but they are not a Noise implementation and have not had an external audit. Test vectors in [spec/test-vectors.json](spec/test-vectors.json) are shared by the Go server and the Android app.

## What is out of scope

* Anyone with access to your PC account can drive your phones, as with any automation tool you run.
* Droidline does not unlock PIN, pattern or password lock screens.
* The accessibility service can read what is on screen. That is how it works; install the app only on phones you control.
