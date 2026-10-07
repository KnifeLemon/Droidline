<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/droidline-logo-dark.png">
    <img src="docs/assets/droidline-logo.png" alt="Droidline" width="300">
  </picture>
</p>

# Droidline

**English** · [한국어](README.ko.md) · [简体中文](README.zh-CN.md)

Control Android phones from any language with one line per action. No ADB, no USB cable, no root.

```python
from droidline import connect

d = connect()                                   # the phone paired with this PC
d.launch("com.kakao.talk")
d.dump("screen.json")                           # look up ids and text here
d.touchById("com.kakao.talk:id/login")
d.input("id", "com.kakao.talk:id/email", "knife")
d.sendkey("enter")
if d.exists("text", "광고 닫기"):
    d.touch("text", "광고 닫기")
d.tap(540, 1200)                                # coordinates only with tap
d.batch([("airplane", True), ("sleep", 3000), ("airplane", False)])  # new IP
```

Site and docs: <https://droidline.dev>

## How it works

```
your code ── SDK / CLI / MCP / curl ──► droidline serve (PC) ◄── Droidline app (phone)
                localhost:8780                               the phone dials out
```

* The phone runs one app: an accessibility service, a keyboard, and an optional per-app VPN. It connects out to the PC, so nothing on the phone listens and nothing needs `adb forward`.
* The PC runs `droidline serve`. Your code talks to it on `localhost:8780`, one JSON line per command.
* Waiting, retries and the coordinate fallback happen on the phone and the server. `touch` waits up to 10 seconds for its target, and if the element refuses the click it taps the center of its bounds instead.
* SDK functions, wire commands, CLI subcommands and MCP tools share one name each, generated from [`spec/commands.json`](spec/commands.json).

The phone can be on the same Wi-Fi, behind a forwarded port, behind a tunnel, or on mobile data through a relay you deploy yourself. Past the handshake every line is encrypted end to end, so a relay or tunnel only carries ciphertext. Details: [`spec/PROTOCOL.md`](spec/PROTOCOL.md).

## Quick start

1. **PC server.** Download `droidline` for your OS from [Releases](https://github.com/KnifeLemon/Droidline/releases), or build it with Go 1.26+:
   ```bash
   go install github.com/KnifeLemon/Droidline/server/cmd/droidline@latest
   ```
   Then run it and leave it open:
   ```bash
   droidline serve
   ```
2. **Phone app.** Install `droidline-agent.apk` from [Releases](https://github.com/KnifeLemon/Droidline/releases). The app is not on Google Play. Open it and follow the checklist: accessibility, the Droidline keyboard, notification permission, battery optimization. On Android 13 and later, a sideloaded app needs "Allow restricted settings" from its App info screen before accessibility can be turned on; the app shows where.
3. **Pair.** On the PC:
   ```bash
   droidline pair
   ```
   Scan the QR code with the app. Without a camera, tap "Pair on this Wi-Fi" in the app and type the 6-digit code it shows: `droidline pair 482913`.
4. **First command.**
   ```bash
   droidline touch text "Settings"
   ```

No phone at hand? `droidline-fakephone` simulates one with a small demo app, so you can try the SDKs, CLI and MCP first:

```bash
go install github.com/KnifeLemon/Droidline/server/cmd/droidline-fakephone@latest
droidline-fakephone            # prints a code; run droidline pair <code>
droidline launch dev.droidline.demo
```

## Use it from your language

| Interface | Install | Example |
|---|---|---|
| Python | `pip install droidline` | `connect().touch("text", "OK")` |
| Node.js / TypeScript | `npm install droidline` | `await (await connect()).touch("text", "OK")` |
| CLI | comes with the server | `droidline touch text OK` |
| HTTP | nothing | `curl -X POST localhost:8780/devices/_/touch -H 'content-type: application/json' -d '{"by":"text","value":"OK"}'` |
| MCP | comes with the server | `droidline mcp` |
| Any language | a TCP socket | send `{"id":1,"cmd":"touch","by":"text","value":"OK"}` and read one line back |

MCP client configuration (Claude Desktop, Claude Code, Cursor and others):

```json
{ "mcpServers": { "droidline": { "command": "droidline", "args": ["mcp"] } } }
```

## Pick the element, not the pixel

`dump()` returns the screen as a tree. Every node has `text`, `id`, `desc`, `class` and `bounds`, and those names are what you pass as the first argument:

| `by` | matches | shorthand |
|---|---|---|
| `text` | text, exact | `touchByText` |
| `textContains` | text, substring | |
| `id` | resource-id; `"login"` also matches `"<package>:id/login"` | `touchById` |
| `desc` | content-desc, exact | `touchByDesc` |
| `descContains` | content-desc, substring | |
| `class` | class name, usually with `nth` | |

Checks such as `exists`, `which`, `checked`, `get_text` and `in_app` answer immediately and never throw for a missing element, so they fit straight into an `if`.

## What it cannot do

Droidline only uses permissions an ordinary app can get. That rules some things out, and it is better to know now:

* It cannot open another app's screens that the app does not export. `launch` falls back to the app's start screen.
* Force stop, clear data, and the mobile data, Wi-Fi and airplane toggles work by opening Settings and pressing the buttons. Each one takes a few seconds, and the button labels differ between manufacturers. Samsung and Pixel phones are the reference.
* Games, some WebViews and custom UIs expose no accessibility nodes. Use `tap` with coordinates and `color(x, y)` there.
* Android 15 and later hide one-time codes in notifications from apps. `wait_notification` tells you a code arrived; read it from the app with `get_text`.
* The per-app proxy needs Android 10 or later, and only one VPN can be active on a phone. Apps that ignore the system proxy lose network access instead of leaking around it.
* Sending key codes or typed text with `sendkey`, and reading the clipboard, need the Droidline keyboard to be the selected input method.

## Repository

| Path | What |
|---|---|
| [`spec/`](spec) | `commands.json` (every command), `PROTOCOL.md`, crypto test vectors |
| [`server/`](server) | Go: `droidline` (server, CLI, MCP), `droidline-relay`, `droidline-fakephone` |
| [`agent/`](agent) | Android app (Kotlin) |
| [`sdk/python`](sdk/python), [`sdk/node`](sdk/node) | official SDKs, generated from the spec plus a thin client |
| [`relay/worker`](relay/worker) | relay for Cloudflare Workers |
| [`scripts/gen.mjs`](scripts/gen.mjs) | generates the SDK methods from `commands.json` |

Build and test everything:

```bash
go test ./spec/ ./server/...
node scripts/gen.mjs --check
cd sdk/python && python -m pytest
cd sdk/node && npm ci && npm test
cd agent && ./gradlew assembleDebug testDebugUnitTest
```

## Status

Version 0.1, before a first public release. The server, CLI, MCP adapter, relay and both SDKs pass their test suites against the phone simulator. The Android app builds and passes its unit tests; device testing across manufacturers is the next step, and settings macros in particular need reports from real phones. See [`docs/STATUS.md`](docs/STATUS.md) for what was verified and how.

Security issues: see [SECURITY.md](SECURITY.md). Use Droidline on phones and accounts you own or are allowed to automate; the [terms](https://droidline.dev/terms/) say what it is for.

## License

MIT. See [LICENSE](LICENSE).
