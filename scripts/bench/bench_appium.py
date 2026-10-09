"""Appium UiAutomator2 on the same screen and actions as bench.py."""
import json
import statistics
import sys
import time

from appium import webdriver
from appium.options.android import UiAutomator2Options
from appium.webdriver.common.appiumby import AppiumBy

# Usage: python bench_appium.py <name for the results file> <adb serial> <label on the Settings home screen> [runs]
# Needs an Appium server with the UiAutomator2 driver on http://127.0.0.1:4725.
name, serial, LABEL = sys.argv[1], sys.argv[2], sys.argv[3]
N = int(sys.argv[4]) if len(sys.argv) > 4 else 10
XPATH = f"//*[@text='{LABEL}']"
out = {"phone": name, "n": N, "results": {"appium": {}}}


def timed(fn):
    t0 = time.perf_counter()
    fn()
    return (time.perf_counter() - t0) * 1000


def record(action, samples):
    out["results"]["appium"][action] = {
        "median_ms": round(statistics.median(samples), 1),
        "min_ms": round(min(samples), 1),
        "max_ms": round(max(samples), 1),
    }
    print(f"appium     {action:22s} median {statistics.median(samples):8.1f} ms  (min {min(samples):.1f}, max {max(samples):.1f})")


def options():
    o = UiAutomator2Options()
    o.set_capability("appium:udid", serial)
    o.set_capability("appium:noReset", True)
    o.set_capability("appium:newCommandTimeout", 120)
    o.set_capability("appium:disableSuppressAccessibilityService", True)
    return o


starts = []
for i in range(2):
    t0 = time.perf_counter()
    drv = webdriver.Remote("http://127.0.0.1:4725", options=options())
    starts.append((time.perf_counter() - t0) * 1000)
    if i == 0:
        drv.quit()
print(f"appium     session start          first {starts[0]:.0f} ms, second {starts[1]:.0f} ms")
out["results"]["appium"]["session start"] = {"first_ms": round(starts[0]), "second_ms": round(starts[1])}

drv.activate_app("com.android.settings")
time.sleep(2)
record("read screen tree", [timed(lambda: drv.page_source) for _ in range(N)])
record("find by text", [timed(lambda: drv.find_element(AppiumBy.XPATH, XPATH)) for _ in range(N)])
taps = []
for _ in range(N):
    taps.append(timed(lambda: drv.find_element(AppiumBy.XPATH, XPATH).click()))
    time.sleep(1.5)
    drv.back()
    time.sleep(1.5)
record("find and tap", taps)
record("screenshot", [timed(lambda: drv.get_screenshot_as_png()) for _ in range(N)])
drv.quit()
open(f"results-{name}-appium.json", "w", encoding="utf-8").write(json.dumps(out, ensure_ascii=False, indent=1))
