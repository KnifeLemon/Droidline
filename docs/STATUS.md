# What has been verified

Last updated 2026-10-09, version 0.1.2.

The Settings macros (`kill`, `wifi`, `data`, `airplane`, and `clear_data` through Settings) were removed on 2026-10-09, because every phone maker, Android version and language needed its own labels. Opening a Settings page is now the general `intent` command, the rest are recipes in the docs, and `clear_data` works only in device owner mode. The emulator and MEmu sections below list the macros as they were tested then.

## Automated tests

| Part | Command | Result |
|---|---|---|
| Spec loading, argument normalisation (including `device` as a parameter of `rename`, `revoke` and `subscribe`, query objects in `by`, and per-command overrides of shared parameters), error templates, and the selector cases in `spec/query-vectors.json` | `go test ./spec/` | pass |
| Key schedule, envelope, tickets against `spec/test-vectors.json` | `go test ./server/internal/dlcrypto/` | pass |
| Envelope split and join over 512 KiB | `go test ./server/internal/agentlink/` | pass |
| Server end to end against the phone simulator: code and QR pairing, single-use QR token, every command group, error rendering, pipelined order, network-cutting batches with and without `wait`, offline wait, agent restart, notifications and `wait_notification`, local HTTP guard (Origin, Host, content type), screenshot over HTTP, a phone reconnecting while its old link still looks alive, query selectors, `find`, `find_all`, `wait_idle`, leases, `find_image` and `tap_image`, OCR without Tesseract, and the WebDriver bridge over HTTP | `go test ./server/internal/server/` | pass |
| Server, relay and phone simulator end to end through `droidline-relay` | `TestThroughRelay` in the same package | pass |
| Template matching and Tesseract output parsing | `go test ./server/internal/vision/` | pass |
| WebDriver bridge: UiSelector parsing, CSS selectors, page source, XPath with axes and from an element | `go test ./server/internal/webdriver/` | pass |
| Inspector: selector suggestions, code in three languages, merging recorded taps and typing | `go test ./server/internal/inspect/` | pass |
| Python SDK | `cd sdk/python && python -m pytest` | 26 passed |
| Node SDK | `cd sdk/node && npm test` | 26 passed |
| Generated SDK code matches `commands.json` | `node scripts/gen.mjs --check` | up to date |
| Cloudflare Worker relay: tickets, frame routing, rate limit | `cd relay/worker && npx vitest run` | 40 passed; `wrangler deploy --dry-run` bundles |
| Android app: crypto vectors, envelope, resume buffer, command cache, relay pacing, QR parsing, selector matching and the shared query cases, key names, SOCKS5 and HTTP CONNECT upstreams | `cd agent && ./gradlew testDebugUnitTest` | 86 passed |
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
* Device owner mode: `dpm set-device-owner` succeeds, `clear_data` wipes Chrome directly (`via: device_owner`, which the Settings path cannot do because Chrome shows Manage space), uninstalling is blocked while the mode is on, and **Turn off** in the app ends it.

Bugs found this way and fixed: the server crashed when a phone reconnected while its previous link still looked alive; the pairing QR escaped the commas between addresses; the macros gave up too early on a slow device; `current` could answer empty right after an app switch.

Later on the same emulator: renaming a phone with `droidline rename` now reaches the app at once (the server sends `renamed`, and `welcome` carries the current name), checked by renaming `shelf-01` to `shelf-02` and back while reading the app's Status tab through Droidline. The app's new icons and colors were checked on all three tabs.

## On MEmu (Android 9, Korean system language)

MEmu 9 (API 28), 800 x 1200, Korean UI. adb was used to install the app, switch on its permissions and forward the agent port; everything else went through Droidline.

Working:

* The app in Korean, every Setup button, pairing from a QR link, `rename`.
* Device, app, element and text commands on the Korean Settings app, including `scroll_to`, `which`, `count`, `input`, `clear` and `sendkey`.
* Screenshots through MediaProjection after the one-time consent, also right after a restart when "don't show again" was ticked.
* `tap`, `long_tap`, `swipe`, `color`, `clipboard`, `open_notifications`, `quick_settings`, `recents`, `lock`, `kill`, `clear_data`, `open_url`, `chrome.go`, `airplane` and `wifi` off and on in one batch, `batch` with `wait`, `proxy` answering `UNSUPPORTED` on Android 9.
* Notifications listed and streamed to the Python SDK, including the ones queued while the phone was offline.
* Query selectors (`row`, `has`, `inside`, `below`, `textMatches`), `find`, `find_all` and `wait_idle` on Settings; element objects in the Python SDK, including a stale element after leaving the screen.
* `find_image` and `tap_image` with an icon cut from a screenshot (score 1.0).
* Leases from the CLI, the Python SDK and the pytest plugin, and `release -d` for a phone a crashed session held.
* The pytest plugin: the failure report lists the phone's commands, and `--droidline-artifacts` saved the command log and screen tree.
* The WebDriver bridge with Appium-Python-Client 4.2.1: page source, XPath with sibling axes, UiSelector with `childSelector` and `UiScrollable`, class name, accessibility id, element text, attributes, rect and screenshots, a W3C swipe, `terminate_app`, `query_app_state`, `mobile: droidline`, and the session ending after `newCommandTimeout`.
* `droidline inspect`: screenshot and tree, picking on the screenshot and in the tree, suggestions with code, **Try it**, the outline view without screen capture permission, the recorder for taps and keyboard typing, and a recorded script replayed on the phone. Checked at desktop and phone width, in light and dark themes.

Bugs found this way and fixed: readiness could stay stale after an app restart; the first screenshot on Android 9 and 10 blocked the phone for 60 seconds instead of answering `NO_PERMISSION`; a second screenshot of a still screen timed out; `current` reported an old activity under the notification shade and an empty one for Droidline's own app; `clear_data` missed the Android 9 Korean labels and could tap the storage permission row; `row` missed a switch in a short list.

Known behaviour: `wake` cannot remove a password lock (Android's own `wm dismiss-keyguard` cannot either); clipboard reads come back empty while a password lock is on; turning Wi-Fi off on its own cuts MEmu's adb and Droidline's link, so turn it off and on in one batch; text set by `input` is not recorded, only typing.

## On real phones

Two Samsung phones in Korean system language, with the debug APK and `droidline serve` on the PC. adb was used to install the app, switch on its permissions and forward the agent port; everything else went through Droidline.

* Galaxy S23 Ultra (SM-S918N), Android 15, 1440 x 3088, adb over Wi-Fi.
* Galaxy Note10 (SM-N976N), Android 12, 1080 x 2280, adb over USB.

Working on both:

* Pairing from a `droidline://pair` link, and `rename`.
* Device, app, element and text commands on Samsung Settings, including query selectors (`row`, `has`, `below`, `textMatches`, `visible`), `find`, `find_all`, element objects and a stale element, `scroll_to`, `which`, `count`, `checked`, `input`, `clear`, `sendkey`, and the `NOT_FOUND` and `NO_IME` messages.
* Accessibility screenshots as PNG and as scaled JPEG, `dump`, `tap`, `long_tap`, `swipe`, `color`, `orientation`, `clipboard` (put back afterwards), `open_notifications`, `quick_settings`, `recents`, `apps`, `installed`, `open_url`, `chrome.go`, `intent`, the force stop recipe, and `clear_data` answering `NO_PERMISSION` outside device owner mode.
* Notifications: listing, `notify_filter`, `wait_notification`, `has_notification`, `notification_dismiss`, and live events in the Python SDK, with test notifications posted from adb.
* `find_image` and `tap_image` (score 0.999), and `ocr` answering `OCR_UNAVAILABLE` without Tesseract.
* The recorder on Android 12 and 15 (taps, typing, long press), the WebDriver bridge with Appium-Python-Client, leases, the pytest plugin with `--droidline-lease` and `--droidline-artifacts`, and the Node SDK. `droidline inspect` on the Note10.

On the Note10 only, because it was on USB and cutting its network did not cut the test link: the Wi-Fi, airplane mode and mobile data recipes (off and on in one `batch` with `cuts_network` and `wait`), the clear data recipe on Samsung Tips, and `proxy` for Chrome through a SOCKS5 server on the PC (Chrome's connections reached that server and Settings stayed direct), with `proxy_check`, `proxy off`, and `NO_PERMISSION` before the VPN permission. Not tried on these phones: `lock` and device owner mode.

Bugs found this way and fixed:

* `wait_idle` returned while Android 15 was still sliding a new page in, so coordinates read at that moment were off screen. It now also waits for positions to stop moving.
* `get_text` right after `input` or `clear` could return the old text. Both now return after the app reports the change.
* Lists keep items just past the screen edge in the tree, with bounds that can be upside down. `scroll_to` stopped on them, and tapping their centre hit the navigation bar. Elements now carry `visible`, `scroll_to` keeps going until the target is on screen and brings a half-shown item fully in, the WebDriver `displayed` attribute follows it, and queries can ask for `"visible": true`.
* `proxy` answered `TIMEOUT` and closed the proxy even when the VPN had started.
* `intent` only brought an open Settings task to the front, so the new page never showed. It now opens the page fresh.
* Before they were removed, the Settings macros needed two more per-phone fixes in this round: Samsung's Korean airplane label "비행기 탑승 모드", and a storage item below the bottom of a smaller screen. That is why they became recipes.

Known behaviour: `wait_notification` and notification events only see apps chosen with `notify_filter`, and the default is none; the reference now says so. `checked` answers `false` when nothing matches. Settings recipes cannot work while the phone is locked; their first `find` or `touch` answers `NOT_FOUND` on `com.android.systemui`. Samsung asks to confirm when mobile data goes off and on, which the recipe handles with the dialog's `android:id/button1`.

## Examples in the docs

The tutorial script in Python, Node.js, bash and PowerShell, the snippets in the Python, Node.js, CLI and HTTP guides, and the Go, Java, C# and PHP examples on the "Other languages" page were run against `droidline serve` and the phone simulator, and their output was copied from those runs. Running them found that `droidline rename <id> <name>` failed with `BAD_ARGS` because the server treated `device` only as the target phone; that is fixed and covered by a test.

## Not verified yet

* Phones other than Samsung. The Settings recipes give labels for English and Chinese, but they were only run on Korean Samsung phones.
* Android 10, 11, 14 and 16.
* `notification_reply` and `notification_click` on real messenger notifications.
* The `direct` (TLS), `tunnel` and `relay` routes from a real phone. The relay route is covered only with the simulator.
* Pairing by QR camera scan and by 6-digit code from a real phone on Wi-Fi.
* OCR with Tesseract installed. Only the missing-Tesseract error and the output parser are tested.
* WebDriver clients other than the Appium Python client: Appium Inspector, WebdriverIO, the Java and .NET clients, Robot Framework.
* `pytest-xdist` with `--droidline-lease` across several real phones.
