# Droidline relay for Cloudflare Workers

Use this relay when neither the PC nor the phones can accept incoming connections, for example phones on LTE/5G behind carrier NAT and a PC behind a home router; if the phones can reach the PC on the same LAN, at a public address, or through a tunnel such as Cloudflare Tunnel or ngrok, you do not need it.

The PC (`droidline serve`) and each phone open an outbound WebSocket to the relay. The relay pairs them by server id and device id and passes text frames between them. It holds no keys: apart from the two handshake lines (ids, public keys, nonces), everything it forwards is an AES-GCM envelope it cannot open. See [PROTOCOL.md section 6](../../spec/PROTOCOL.md#6-relay).

The Droidline project runs no relay of its own. You deploy this one to your own Cloudflare account.

## Deploy

You need a Cloudflare account. The Workers Free plan is enough for a handful of phones (see [Free plan limits](#free-plan-limits)).

### With the button

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/KnifeLemon/Droidline/tree/main/relay/worker)

The deploy page copies this directory into a new repository in your GitHub or GitLab account, asks for `RELAY_TOKEN`, and deploys the Worker with its Durable Object.

### With Wrangler

```sh
git clone https://github.com/KnifeLemon/Droidline
cd droidline/relay/worker
npm install
npx wrangler login
npx wrangler deploy
npx wrangler secret put RELAY_TOKEN
```

`wrangler deploy` prints the Worker's address, for example `https://droidline-relay.<your-subdomain>.workers.dev`. To use another name, change `name` in `wrangler.jsonc` before deploying.

### RELAY_TOKEN

`RELAY_TOKEN` is the shared secret between the relay and your PC. Use a long random value:

```sh
node -e "console.log(require('crypto').randomBytes(32).toString('base64url'))"
```

Store it as a secret (`npx wrangler secret put RELAY_TOKEN`), not in `wrangler.jsonc`. Until it is set, the relay answers every connection with `503`.

Phones never see this token. The PC derives a ticket for each phone from it (PROTOCOL.md 6.2) and hands the ticket over in the pairing QR or the `config` event. Changing the token invalidates every ticket derived from the old value.

## Point the PC at the relay

```sh
droidline relay set https://<your-worker>.workers.dev <token>
```

The PC then connects to the relay, includes the relay address and an enrollment ticket in new pairing QR codes, and sends paired phones their device tickets.

## Check that it is up

```sh
curl https://<your-worker>.workers.dev/healthz
```

It prints `ok`.

## Free plan limits

Durable Objects on the Workers Free plan allow 100,000 requests per day, and incoming WebSocket messages count toward requests at a 20:1 ratio (20 messages count as one request). Outgoing messages and WebSocket protocol pings are not counted. Opening a WebSocket counts as one request. Once a daily limit is reached, further operations of that type fail with an error until the limit resets. Source: [Durable Objects pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/).

For scale, an idle phone costs about 350 requests per day: the phone sends a `ping` every 25 seconds and the PC answers with a `pong`, which is two incoming messages at the relay, or 6,912 messages a day. Commands, results, events and screenshots add one incoming message per line (a screenshot over 512 KiB is split into several lines).

The relay uses the WebSocket Hibernation API, so Cloudflare does not bill duration while it is idle. On the Free plan, Durable Objects must use the SQLite storage backend, which the `new_sqlite_classes` migration in `wrangler.jsonc` selects. The relay stores nothing in it.

## Routes

| Request | Who | Bearer |
|---|---|---|
| `GET /v1/server?server=<id>` | the PC | `RELAY_TOKEN` |
| `GET /v1/device?server=<id>&device=<id>` | a paired phone | device ticket |
| `GET /v1/device?server=<id>&device=<id>&expiry=<unix seconds>` | a phone that is pairing | enrollment ticket, refused after `expiry` |
| `GET /healthz` | anyone | none, returns `ok` |

The bearer goes in the `Authorization: Bearer ...` header. A `token` query parameter is not accepted: the PC and the Android agent can both set headers on a WebSocket upgrade, and a token in a URL ends up in access logs.

`server` and `device` must match `^[a-z0-9]{1,32}$`. Each server id gets its own Durable Object.

| Status | Meaning |
|---|---|
| `101` | WebSocket accepted |
| `400` | malformed `server`, `device` or `expiry` |
| `401` | missing or wrong bearer, or an expired enrollment ticket (body `ticket expired`) |
| `404` | unknown path or a method other than `GET` |
| `409` | an enrollment connection for a device that is already connected with a device ticket |
| `426` | a plain HTTP request to a WebSocket route |
| `503` | `RELAY_TOKEN` is not set |

## Frames

Phone to PC: each text frame from a phone reaches the PC as `{"device":"<id>","line":"<frame>"}`. The line stays a string, so the PC gets back the exact text the handshake hash covers.

PC to phone: `{"device":"<id>","line":"..."}` delivers `line` to that phone as a raw text frame. `{"device":"<id>","close":true}` closes that phone's connection; a frame carrying both delivers the line first. Frames the relay cannot route are ignored.

Events to the PC: `{"device":"<id>","event":"open"}` when a phone connects, and `{"device":"<id>","event":"close"}` once when that connection ends for any reason. When a phone reconnects, the `close` for the old connection arrives before the `open` for the new one. When the PC connects, it gets an `open` for every phone already connected. While no PC is connected, phone frames are dropped and the phone stays connected; the phone's resume buffer (PROTOCOL.md 4.8) replays them later.

## Close codes

| Code | Sent to | Reason |
|---|---|---|
| `1000` | phone | the PC asked to close it |
| `1003` | either | a binary frame (the protocol is text only) |
| `1009` | either | a line over 1 MiB, the protocol limit |
| `4000` | either | a newer connection with the same server id, or the same device id, replaced this one |
| `4008` | phone | more than 50 frames per second after a burst of 200 |

Cloudflare itself accepts larger messages (32 MiB per received message); the 1 MiB check comes from the protocol. The rate limit lives in Durable Object memory and starts fresh after the object hibernates. A dropped frame would break the envelope sequence anyway, so the relay closes the connection instead of discarding frames. A phone replaying a large resume buffer should pace itself under 50 frames per second.

An enrollment ticket is not tied to a device id, so the relay refuses (`409`) an enrollment connection for a device id that is connected with a device ticket. Without this, anyone holding a pairing QR could push paired phones off the relay until the ticket expires.

## Logs

The relay logs one line per connect and disconnect with the number of connected PCs and phones. It does not log frame contents, ids or addresses. View the lines with `npx wrangler tail`.

## Development

```sh
npm install
npm run check   # tsc --noEmit
npm test        # vitest: tickets against spec/test-vectors.json, frame routing, rate limit
npx wrangler dev --var RELAY_TOKEN:<token>
npx wrangler deploy --dry-run --outdir dist
```

`npm test` reads `../../spec/test-vectors.json`, so run it from a full checkout of the repository.
