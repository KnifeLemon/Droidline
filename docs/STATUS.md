# What has been verified

Last updated 2026-10-07, version 0.1.0 (unreleased).

## Automated tests

| Part | Command | Result |
|---|---|---|
| Spec loading, argument normalisation (including `device` as a parameter of `rename`, `revoke` and `subscribe`), error templates | `go test ./spec/` | pass |
| Key schedule, envelope, tickets against `spec/test-vectors.json` | `go test ./server/internal/dlcrypto/` | pass |
| Envelope split and join over 512 KiB | `go test ./server/internal/agentlink/` | pass |
| Server end to end against the phone simulator: code and QR pairing, single-use QR token, every command group, error rendering, pipelined order, network-cutting commands with and without `wait`, offline wait, agent restart, notifications and `wait_notification`, local HTTP guard (Origin, Host, content type), screenshot over HTTP, a phone reconnecting while its old link still looks alive | `go test ./server/internal/server/` | pass |
| Server, relay and phone simulator end to end through `droidline-relay` | `TestThroughRelay` in the same package | pass |
| Python SDK | `cd sdk/python && python -m pytest` | 22 passed |
| Node SDK | `cd sdk/node && npm test` | 22 passed |
| Generated SDK code matches `commands.json` | `node scripts/gen.mjs --check` | up to date |
| Cloudflare Worker relay: tickets, frame routing, rate limit | `cd relay/worker && npx vitest run` | 40 passed; `wrangler deploy --dry-run` bundles |
| Android app: crypto vectors, envelope, resume buffer, command cache, relay pacing, QR parsing, selector matching, key names, macro labels, SOCKS5 and HTTP CONNECT upstreams | `cd agent && ./gradlew testDebugUnitTest` | 90 passed |
| Both SDK demos against a real server and the simulator | `scripts/integration.sh` | pass |

## On an Android emulator

Pixel 8 virtual device, Android 13 (API 33), x86_64, English system language, the debug APK, and `droidline serve` on the host. adb was used only to install the app and switch on its permissions; every command below went through Droidline.

Working:

* Pairing from a `droidline://pair` link (QR token mode), so the Go server and the Kotlin app agree on the handshake and the encryption.
* `info`, `current`, `home`, `back`, `launch`, `dump`, `screenshot` (accessibility screenshot), `touch` with the parent fallback, `get_text`, `exists`, `which`, `checked`, `scroll_to`, `clipboard` write and read through the Droidline keyboard, `sendkey`, `keyboard_shown`, `color`, `battery`, `network`, `orientation`, `installed`, `apps`, `lock`, `screen_on`, `wake`, `chrome.go`.
* Settings macros: `kill`, `clear_data`, `wifi`, `data`, `airplane`.
* `batch` with airplane on, sleep, airplane off and `wait`: the phone ran the batch while offline, reconnected and delivered the result.
* Notifications: `notify_filter`, `wait_notification`, `has_notification`, `notification_dismiss`.
* Error messages rendered in Korean by the server, naming the current screen.
* `proxy` without VPN permission answers `NO_PERMISSION`.
* The Python SDK driving Settings.

Bugs found this way and fixed: the server crashed when a phone reconnected while its previous link still looked alive; the pairing QR escaped the commas between addresses; the macros gave up too early on a slow device; `current` could answer empty right after an app switch.

Later on the same emulator: renaming a phone with `droidline rename` now reaches the app at once (the server sends `renamed`, and `welcome` carries the current name), checked by renaming `shelf-01` to `shelf-02` and back while reading the app's Status tab through Droidline. The app's new icons and colors were checked on all three tabs.

Known behaviour: `clear_data` cannot clear apps that replace "Clear storage" with their own "Manage space" screen (Chrome is one). It fails with step `manage_space`.

## Examples in the docs

The tutorial script in Python, Node.js, bash and PowerShell, the snippets in the Python, Node.js, CLI and HTTP guides, and the Go, Java, C# and PHP examples on the "Other languages" page were run against `droidline serve` and the phone simulator, and their output was copied from those runs. Running them found that `droidline rename <id> <name>` failed with `BAD_ARGS` because the server treated `device` only as the target phone; that is fixed and covered by a test.

## Not verified yet

* Real phones. Samsung and Pixel are the reference; settings macros on other brands and in Korean or Chinese system language need reports.
* Android 9, 10, 11, 12, 14, 15 and 16. In particular screen capture through MediaProjection on 9 and 10.
* The per-app proxy on a device (needs the VPN permission granted).
* `notification_reply` and `notification_click` on real messenger notifications.
* The `direct` (TLS), `tunnel` and `relay` routes from a real phone. The relay route is covered only with the simulator.
* Pairing by QR camera scan and by 6-digit code from a real phone on Wi-Fi.
