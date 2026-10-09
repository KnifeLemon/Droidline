# Speed comparison

These scripts time the same four actions on the same phone screen three ways: Droidline, adb with `uiautomator`, and Appium with the UiAutomator2 driver. The speed section on droidline.dev shows the results in `results-2026-10-09.json`.

| Action | Droidline | adb | Appium UiAutomator2 |
|---|---|---|---|
| Read the screen tree | `dump` | `uiautomator dump`, then `cat` the file | `page_source` |
| Find by text | `find` | read the tree and search it | `find_element` by XPath |
| Find and tap | `touch` | read the tree, then `input tap` | `find_element(...).click()` |
| Screenshot | `screenshot` | `exec-out screencap -p` | `get_screenshot_as_png` |

Each action runs ten times and the median is kept, along with the fastest and slowest run. Run the methods one after another, never at the same time: `uiautomator dump` and Appium both use Android's UiAutomation, which pauses other accessibility services while it runs.

## Run it

```bash
pip install droidline Appium-Python-Client
npm install appium
npx appium driver install uiautomator2
npx appium --port 4725

# in another terminal, with Settings open on its home screen
python bench.py <droidline name> <adb serial> "<a label on that screen>"
python bench_appium.py <name> <adb serial> "<a label on that screen>"
```

Appium installs its helper apps on the phone the first time. Remove them afterwards with `adb uninstall io.appium.uiautomator2.server`, `adb uninstall io.appium.uiautomator2.server.test` and `adb uninstall io.appium.settings`.

## What the 2026-10-09 run used

* Galaxy S23 Ultra (Android 15) with adb over Wi-Fi, and Galaxy Note10 (Android 12) with adb over USB. Both in Korean, on the Settings home screen, with the label `연결`.
* Droidline's link to the PC ran through `adb reverse` on the same connection, so all three methods shared one transport.
* Appium 3.8.0, UiAutomator2 driver 8.7.0, Appium-Python-Client 4.25.0. Starting an Appium session is not in the table; it took 4.3 to 5.9 seconds.
