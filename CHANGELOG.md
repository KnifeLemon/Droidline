# Changelog

What changed in each version of Droidline, for the people using it. The release workflow puts the section of the
version it publishes under "What's Changed" in the GitHub release notes, above the list of pull requests.

Add a `## <version>` section before tagging a release. Write what someone using Droidline notices: what's new, how to
call it, what it does and doesn't do.

## 0.1.5

### New

- `droidline.mcpb` on each release: a bundle for Claude Desktop and other MCP clients that carries `droidline` for
  Windows x64, macOS (Intel and Apple silicon) and Linux x64. Open it and the phone's commands appear as tools, with no
  separate install. On Linux arm64, use the release archive and `droidline mcp`.
- Droidline is listed in the MCP Registry as `io.github.KnifeLemon/droidline`.

## 0.1.4

### Changed

- The NuGet package shows the Droidline icon, and the package pages on PyPI, npm and NuGet open with the Droidline
  banner.

## 0.1.3

### New

- A .NET SDK on NuGet: `dotnet add package Droidline`, with the same commands as the Python and Node.js SDKs.

## 0.1.2

### New

- The SDKs are on the package registries: `pip install droidline` for Python and `npm install droidline` for
  Node.js. Both are published from this repository's release workflow.

## 0.1.1

### New, all optional

Nothing below runs or changes until you call it.

- Query selectors: pass an object as `by` to combine conditions, or to name an element by its neighbours, such as
  `{"class": "android.widget.Switch", "row": {"text": "Wi-Fi"}}`. The keys are `has`, `inside`, `row`, `below`,
  `above`, `left_of`, `right_of`, regular expressions on text and desc, the element flags, and `visible`, which skips
  list items Android keeps just past the edge of the screen.
- `find` and `find_all` return elements you can click, type into and search inside. `wait_idle` waits until the
  screen stops changing.
- `intent` opens any screen other apps may open, such as a Settings page.
- `batch` takes `cuts_network` for steps that drop the phone's link, such as Wi-Fi off and on again. The phone runs
  the steps while offline and the result arrives after it reconnects.
- `droidline inspect` shows the phone's screen and elements in a browser, suggests selectors with code in Python,
  Node.js and the CLI, and records a script while you use the phone.
- `droidline webdriver` lets Appium clients and Appium scripts drive your phones, with XPath, UiSelector, ids and
  accessibility ids. There is no ADB behind it, so `adb shell`, installing apps and force stop are not there.
- Leases: `droidline lease` and `lease()` borrow a free phone so two scripts never act on the same one.
- Pictures and text on screens without elements, such as games: `find_image` and `tap_image` match a picture, and
  `ocr` reads text with Tesseract, which you install on the PC yourself.
- A pytest plugin (`pytest -p droidline.pytest_plugin`) with a phone fixture, and for each failed test the commands
  it sent, a screenshot and the screen tree. Node.js has the same report helpers in `droidline/testing`.
- Device owner mode: set up once from a computer, and `clear_data` wipes an app directly. The guide on droidline.dev
  shows how to set it up and how to turn it off.
- A new symbol and app colours.

### Changed

- `wifi`, `data`, `airplane` and `kill` are removed. They pressed buttons in Android's Settings app, and every phone
  maker, Android version and language needed its own labels. The recipes page on droidline.dev shows how to do the
  same with `intent`, `touch` and `batch`, checked on Samsung phones.
- `clear_data` works only in device owner mode. Without it the answer is `NO_PERMISSION`, and the recipes page shows
  the Settings route.
- The WebDriver bridge answers `terminate_app` with "unsupported operation", for the same reason.

### Fixes

- `droidline rename` and `revoke` failed with `BAD_ARGS`. The app now shows a new name as soon as you rename the phone.
- On Android 15, `wait_idle` returned while a new page was still sliding in, so coordinates read right after it
  pointed off screen. It now waits until elements stop moving.
- Reading a field right after `input` or `clear` could return the old text.
- `scroll_to` could stop on a list item just past the edge of the screen, and tapping its centre hit the navigation
  bar. It now scrolls until the item is on screen.
- `proxy` answered `TIMEOUT` and turned the proxy off again even when the VPN had started.
- On Android 9 and 10, the first screenshot without screen capture permission blocked the phone for 60 seconds
  instead of answering `NO_PERMISSION`, and a second screenshot of a screen that had not changed could time out.
- `current` could name the app under the notification shade, or an empty activity for Droidline's own app.

## 0.1.0

The first release.

- `droidline serve` on the PC, and the Droidline app on the phone: an accessibility service, a keyboard and an
  optional per-app VPN. The phone connects out to the PC, so nothing on the phone listens and no USB cable or ADB is
  needed.
- Python and Node.js SDKs, a CLI, a local HTTP API and an MCP server for AI agents, with one name per command in all
  of them.
- Pairing by QR code or by a 6-digit code on the same Wi-Fi.
- The phone can be on the same Wi-Fi, behind a forwarded port or a tunnel, or on mobile data through a relay you
  deploy. Every line is encrypted end to end past the handshake.
- Commands for elements, text input, gestures, screenshots, apps, notifications, the clipboard and a per-app proxy.
- `droidline-fakephone`, a simulated phone with a small demo app, to try the SDKs without a phone.
