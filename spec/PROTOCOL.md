# Droidline protocol, version 1

This is the normative description of every wire format in Droidline. The SDKs, the CLI, the MCP adapter and the Android agent are all thin layers over it, so any language that can open a socket and read a line of JSON can drive a phone.

Three parties take part:

| Party | Runs on | Talks to |
|---|---|---|
| **agent** | the Android phone (the Droidline app) | the server, outbound only |
| **server** | the PC (`droidline serve`) | agents and clients |
| **client** | your code: SDK, CLI, MCP adapter, curl | the server, on localhost |

Commands, parameters, return values and error codes are defined once in [`commands.json`](commands.json). This document describes how they travel.

## 1. Framing

* UTF-8 JSON, one object per line, terminated by `\n` (NDJSON). No pretty printing.
* A line is at most 1 MiB. Larger payloads (screenshots, dumps) are split by the encrypted envelope (section 4.5), never by the JSON layer.
* Unknown fields are ignored. Unknown commands are answered with `UNKNOWN_CMD`.
* Integers that can exceed 2^53 (none today) would be sent as strings.

## 2. Ports

| Port | Proto | Bound to | Purpose |
|---|---|---|---|
| 8778 | UDP | all interfaces | discovery |
| 8779 | TCP | all interfaces | agent link (plain NDJSON, TLS, or WebSocket on the same port) |
| 8780 | TCP | 127.0.0.1 | client API (NDJSON, HTTP and WebSocket on the same port) |

The server tells the protocols apart by the first byte a peer sends: `{` is NDJSON, `0x16` is a TLS ClientHello, an ASCII letter is HTTP. Behind TLS the same check runs again. So one forwarded port is enough for every remote route.

## 3. Client API (port 8780)

### 3.1 Requests and responses

```json
{"id":1,"cmd":"touch","device":"shelf-01","by":"id","value":"com.kakao.talk:id/login"}
{"id":1,"ok":true,"via":"node","ms":412}
{"id":2,"cmd":"exists","by":"text","value":"광고 닫기"}
{"id":2,"ok":true,"value":false}
{"id":3,"cmd":"input","by":"text","value":"아이디","text":"knife"}
{"id":3,"ok":false,"error":"NOT_FOUND","msg":"10초 동안 text '아이디' 대상을 찾지 못했습니다. 현재 화면: com.kakao.talk / .LoginActivity","screen":"com.kakao.talk/.LoginActivity","retryable":true}
```

* `id` is chosen by the client (number or string) and echoed back. Requests may be pipelined; responses can arrive out of order across devices.
* `device` is a device ID or name. It may be omitted when exactly one paired device is online; otherwise the server answers `DEVICE_AMBIGUOUS`.
* Parameters are named as in `commands.json`. Positional arrays are also accepted: `{"id":4,"cmd":"tap","args":[540,1200]}` maps `args` onto the parameter order.
* Aliases are accepted as commands: `{"cmd":"touchById","value":"login"}` is `touch` with `by:"id"`.
* A result is spread into the response object. Commands whose `returns.kind` is `value` put it under `value`.
* Errors carry `error` (a code from `commands.json`), `msg` (rendered in the server's language), `retryable`, and the fields the template used (`screen`, `permission`, `candidates`...).

Commands sent to one device run strictly in order. Commands to different devices run in parallel.

If the device is offline the command waits for it to reconnect, up to `offline_wait` seconds (default 30, request field `offline_wait` overrides), then fails with `DEVICE_OFFLINE`.

### 3.2 Network-cutting commands

`data(false)`, `wifi(false)`, `airplane(true)` and a `batch` containing one of them cut the phone's own link. The phone answers before acting:

```json
{"id":7,"ok":true,"accepted":true}
{"event":"result","id":7,"device":"shelf-01","ok":true,"via":"settings_macro","ms":2300}
```

The `result` event arrives once the phone is back. Set `"wait":true` on the request to skip the `accepted` line and receive the final result as the normal response instead (it then also obeys `offline_wait`, counted from the moment of acceptance).

### 3.3 Events

A client receives events only after `subscribe`:

```json
{"id":9,"cmd":"subscribe","events":["notification","device"]}
{"id":9,"ok":true}
{"event":"device","device":"a1b2c3d4","name":"shelf-01","state":"online","route":"lan"}
{"event":"notification","device":"a1b2c3d4","key":"0|com.kakao.talk|1|null|10123","package":"com.kakao.talk","title":"홍길동","text":"내일 3시에 봬요","time":1791360000000,"actions":["reply","mark_read"]}
```

Event kinds: `device` (online/offline), `notification`, `screen` (foreground app changed), `toast`, `result` (late results from section 3.2, always delivered to the connection that sent the command, whether or not it subscribed).

### 3.4 Authentication

Loopback connections need no token unless `client.require_token = true`. When the client port is opened to other machines (`client.listen = "0.0.0.0:8780"`), a token is always required. The first request must then be:

```json
{"id":0,"cmd":"auth","token":"dlc_…"}
```

Tokens are created with `droidline token create` and stored hashed.

### 3.5 Local HTTP

The same port speaks HTTP/1.1 for curl, no-code tools and languages without an SDK. One request, one response.

| Method and path | Meaning |
|---|---|
| `GET /devices` | `devices` |
| `POST /devices/{device}/{cmd}` | run a device command; JSON body holds the parameters |
| `GET /devices/{device}/screenshot.jpg` (or `.png`) | raw image bytes |
| `POST /server/{cmd}` | run a server command (`pair`, `pair_qr`, `rename`, `revoke`) |
| `GET /ws` | WebSocket; each text frame is one NDJSON line of section 3.1 |

`{device}` may be `_` to mean "the only online device".

```sh
curl -s -X POST localhost:8780/devices/_/touch -H 'content-type: application/json' -d '{"by":"text","value":"로그인"}'
```

Status codes: `200` with `ok:true`; on failure the HTTP status from the error's `http` field in `commands.json` with the same JSON body.

Browser protection: the server rejects any request that carries an `Origin` header not listed in `client.allowed_origins`, any `Host` header other than `localhost`, `127.0.0.1` or `[::1]` while bound to loopback (DNS rebinding), and any `POST` whose content type is not `application/json`. A web page you visit therefore cannot drive your phones.

## 4. Agent link (port 8779)

### 4.1 Discovery

The phone broadcasts to `255.255.255.255:8778` (and to each interface's directed broadcast address):

```json
{"droidline":"discover","v":1,"server":"k3j9d0a2mq"}
```

`server` is optional; a paired phone sets it to find its own PC. Every server whose ID matches (or any server, if `server` is absent and pairing is open) replies by unicast to the sender:

```json
{"droidline":"here","v":1,"server":"k3j9d0a2mq","name":"OFFICE-PC","port":8779,"pub":"BD3x…","pairing":true}
```

`pub` is the server's static public key (4.3). The reply is unauthenticated; the handshake authenticates it.

### 4.2 Transports and routes

The phone tries routes in this order and reports the one in use as `route`:

| route | how | encryption on the wire |
|---|---|---|
| `lan` | TCP to the address found by discovery | envelope (4.5) |
| `direct` | TLS to a public address from the pairing QR, certificate pinned by SHA-256 fingerprint | TLS + envelope |
| `tunnel` | WebSocket (`wss://…/agent`) through Cloudflare Tunnel, ngrok and similar | TLS + envelope |
| `relay` | WebSocket to a relay (section 6) | TLS + envelope |

While on any route other than `lan`, the phone retries discovery whenever its Wi-Fi network changes and every 60 seconds; if the PC answers, it opens a `lan` link and closes the old one. The session survives the switch (4.8).

Over WebSocket each text frame is exactly one line without the trailing newline.

### 4.3 Keys

* Curve P-256. Public keys travel as the 65-byte uncompressed point, base64url without padding.
* The server has one static key pair, created on first start. The private key is kept with the OS secret store (DPAPI on Windows; a `0600` file elsewhere).
* Each phone creates its own static key pair at install, kept in the Android Keystore-wrapped app storage.
* A device is identified by `device`, 8 lowercase base32 characters generated at install.

### 4.4 Handshake

Two plaintext lines, then everything is encrypted.

Phone to server:

```json
{"hs":1,"proto":1,"mode":"auth","device":"a1b2c3d4","eph":"BHq…","nonce":"q8D…"}
```

`mode` is one of:

* `auth`: the phone is already paired. Nothing else is added.
* `pair_qr`: pairing with a QR enrollment token. Adds `"pub":"<phone static>"` and `"tid":"<token id>"`.
* `pair_code`: pairing by comparing a 6-digit code. Adds `"pub":"<phone static>"`.

Server to phone:

```json
{"hs":1,"proto":1,"server":"k3j9d0a2mq","status":"ok","eph":"BPa…","nonce":"Zx1…"}
```

`status` other than `ok` ends the connection: `unknown_device` (not paired or revoked), `pairing_closed` (no QR token or code pairing is open), `bad_proto`, `busy`.

`nonce` is 16 random bytes on each side. Key schedule, with `‖` meaning concatenation:

```
TH   = SHA-256(line1 ‖ "\n" ‖ line2)            exact bytes as sent, without trailing newlines
IKM  = ECDH(eph_phone, eph_server) ‖ ECDH(static_phone, static_server) [‖ token]
PRK  = HKDF-Extract(salt = TH, IKM)
k_up = HKDF-Expand(PRK, "droidline v1 up",   32)   phone -> server
k_dn = HKDF-Expand(PRK, "droidline v1 down", 32)   server -> phone
SAS  = HKDF-Expand(PRK, "droidline v1 sas",   4)   as big-endian uint32 mod 1000000, zero-padded to 6 digits
```

`token` (16 bytes) is appended only in `pair_qr` mode. The ephemeral term gives forward secrecy; the static term authenticates both sides, because a party without the right private key derives different keys and the first encrypted line fails to open.

### 4.5 Envelope

After the handshake every line is:

```json
{"seq":0,"blob":"<base64url(AES-256-GCM ciphertext ‖ tag)>"}
```

* Key `k_up` or `k_dn` by direction. Nonce is 12 bytes: four zero bytes, then `seq` as a big-endian uint64.
* `seq` starts at 0 for each direction of each connection and increases by exactly 1. A receiver closes the connection on any other value.
* AAD is the ASCII string `droidline v1`.
* Plaintext is one JSON object (one line of the inner protocol, without newline).
* If `blob` would exceed 512 KiB, the sender splits the base64 text into parts and sends `{"seq":n,"blob":"<part>","more":true}` for all but the last part, all with the same `seq`. The receiver concatenates the parts, then decrypts. `seq` then advances by one.

### 4.6 Session start

The phone's first encrypted line:

```json
{"event":"hello","device":"a1b2c3d4","boot":"7f3a9c","model":"SM-S921N","manufacturer":"samsung","sdk":34,"release":"14","agent":"0.1.0","route":"lan","lang":"ko","ready":{"a11y":true,"ime":true,"vpn":false,"notif":true,"capture":true}}
```

`boot` is random per agent process start. The server replies:

```json
{"event":"welcome","server":"k3j9d0a2mq","name":"OFFICE-PC","lang":"ko","device_name":"shelf-01","ack":41,"ping":25}
```

`ack` is the last `n` (4.7) the server holds from this `boot`; 0 for a new boot. `lang` is the language the server renders error messages in. `device_name` is the phone's current name on the server; when it changes with `rename`, a connected phone receives `{"event":"renamed","name":"shelf-02"}`.

In `pair_code` mode the phone shows `SAS` as a 6-digit code and the server holds the device as pending until the user runs `droidline pair 482913` (or the `pair` command). Then the server sends `{"event":"paired","name":"shelf-01"}`, stores the phone's static key, and continues with `welcome`. In `pair_qr` mode, a successful decrypt of `hello` proves the phone holds the token; the server stores the key and sends `paired` then `welcome` immediately. Pending pairings expire after 5 minutes.

### 4.7 Commands, responses, events

Server to phone:

```json
{"id":17,"cmd":"touch","by":"id","value":"com.kakao.talk:id/login","nth":0,"timeout":10}
```

Phone to server:

```json
{"id":17,"n":42,"ok":true,"via":"node","ms":412}
{"event":"screen","n":43,"package":"com.kakao.talk","activity":".MainActivity"}
```

* The server assigns `id`, unique per device for the life of the server process.
* The phone numbers everything it sends (responses and events) with `n`, starting at 1 per `boot`.
* The phone executes commands one at a time, in arrival order.
* Error responses have the same fields as in section 3.1. The phone sends its own English `msg`; the server re-renders it from the template in `commands.json`.
* Parameters arrive normalised: aliases resolved, positional arguments mapped, defaults filled in. The phone never sees `path` or other `client_only` parameters.

### 4.8 Resume and exactly-once

* The phone keeps every message it sent in a buffer until acknowledged (at most 2000 messages or 8 MiB; oldest dropped first).
* The server acknowledges with `{"event":"ack","n":42}` at least every 5 seconds while traffic flows and in each `pong`.
* After a reconnect, `welcome.ack` tells the phone where to resume; it resends everything after that, in order. The server drops any `n` it has already seen.
* On the `relay` route the phone paces that resend: the first 150 lines at once, then no more than 40 lines per second, so it stays under the relay's rate limit (section 6.2).
* The server resends commands that were in flight when the link dropped, with the same `id`. The phone keeps the last 2000 command IDs with their result (or "running"), so a resent command is answered from the cache instead of running twice.
* If `hello.boot` differs from the previous boot, the old process is gone with its cache. Commands that were in flight fail with `AGENT_RESTARTED` instead of being resent.

### 4.9 Keepalive

The phone sends `{"event":"ping","t":1791360000000}` every 25 seconds (`welcome.ping` can change it) and the server answers `{"event":"pong","t":…,"ack":n}`. Either side closes a link that has been silent for three intervals. The phone reconnects with backoff 1, 2, 4, 8, 16, 30, 30… seconds, and immediately when the OS reports a new network.

### 4.10 Phone-side batch

```json
{"id":20,"cmd":"batch","steps":[{"cmd":"airplane","on":true},{"cmd":"sleep","ms":3000},{"cmd":"airplane","on":false}],"stop_on_error":true}
{"id":20,"n":50,"ok":true,"accepted":true}
{"event":"result","n":51,"id":20,"ok":true,"results":[{"ok":true,"via":"settings_macro","ms":2100},{"ok":true},{"ok":true,"via":"settings_macro","ms":1900}]}
```

The phone answers `accepted` first when any step cuts the network, otherwise it answers with the results directly.

## 5. Pairing QR

```
droidline://pair?v=1&s=<server id>&n=<server name>&k=<server static pub>&t=<tid>.<token>&a=<addr>,<addr>&f=<tls sha256 hex>&r=<relay url>&rt=<relay ticket>&re=<ticket expiry>
```

* `t`: token ID and 16-byte secret, base64url. Valid for 10 minutes, single use.
* `a`: comma-separated, each URL-encoded: `tcp://192.168.0.10:8779`, `tls://203.0.113.5:8779`, `wss://droid.example.com/agent`.
* `f`: SHA-256 of the server's self-signed TLS certificate, required when an address uses `tls://`.
* `r`, `rt`, `re`: relay URL and an enrollment ticket (section 6.2), only when a relay is configured.

After pairing, the server pushes `{"event":"config","addresses":[…],"relay":{"url":"…","ticket":"…"},"tls_fp":"…"}` whenever its addresses change, so phones learn new routes without re-pairing.

## 6. Relay

A relay only forwards lines between a server and its phones. It never holds keys: everything it sees past the handshake is an envelope it cannot open. The project ships the relay as code ([`relay/worker`](../relay/worker) for Cloudflare Workers, `droidline-relay` for a VPS or Docker). Nobody operates a public one.

### 6.1 Server side

```
GET wss://relay.example.com/v1/server?server=<server id>
Authorization: Bearer <relay token>
```

Frames, in both directions, are JSON text:

```json
{"device":"a1b2c3d4","line":"{\"seq\":5,\"blob\":\"…\"}"}
{"device":"a1b2c3d4","event":"open"}
{"device":"a1b2c3d4","event":"close"}
```

`line` is the agent's line as a string, so the relay never re-encodes the bytes the handshake hash covers. The server may send `{"device":"…","close":true}` to drop a phone.

### 6.2 Phone side

```
GET wss://relay.example.com/v1/device?server=<server id>&device=<device id>
Authorization: Bearer <ticket>
```

* Paired phone: `ticket = base64url(HMAC-SHA256(relay_token, "droidline device|" ‖ server ‖ "|" ‖ device))`, pushed by the server in the `config` event.
* Pairing phone: `ticket = base64url(HMAC-SHA256(relay_token, "droidline enroll|" ‖ server ‖ "|" ‖ expiry))` with `&expiry=<unix seconds>` added to the URL. The relay rejects it after `expiry`.

The relay closes a second connection for the same `server` and `device` by dropping the older one (close code 4000), rejects anything without a valid bearer, and limits each device to 50 frames per second with a burst of 200 (close code 4008). An enroll ticket is not bound to a device ID, so it may not replace a phone that is connected with a device ticket: the relay answers 409 instead. A device ticket may replace an enroll connection.

## 7. Versioning

`proto` is 1. A server accepts agents whose `proto` it knows and answers `bad_proto` otherwise. New commands do not bump `proto`; an agent that lacks one answers `UNKNOWN_CMD` with its version.
