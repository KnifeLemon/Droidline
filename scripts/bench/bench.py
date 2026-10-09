"""Same phone, same screen (the Settings home screen), three ways: Droidline, adb with uiautomator, Appium UiAutomator2.

Each action runs N times; the median and the spread are written to results-<phone>.json.
The methods run one after another, never at once: UiAutomation pauses other accessibility services.
"""
import json
import re
import statistics
import subprocess
import sys
import time

from droidline import connect

# Usage: python bench.py <droidline name> <adb serial> <label on the Settings home screen> [runs]
name, serial, LABEL = sys.argv[1], sys.argv[2], sys.argv[3]
N = int(sys.argv[4]) if len(sys.argv) > 4 else 10
out = {"phone": name, "n": N, "results": {}}


def timed(fn):
    t0 = time.perf_counter()
    fn()
    return (time.perf_counter() - t0) * 1000


def record(method, action, samples):
    out["results"].setdefault(method, {})[action] = {
        "median_ms": round(statistics.median(samples), 1),
        "min_ms": round(min(samples), 1),
        "max_ms": round(max(samples), 1),
    }
    print(f"{method:10s} {action:22s} median {statistics.median(samples):8.1f} ms  (min {min(samples):.1f}, max {max(samples):.1f})")


d = connect(name)


def settings_home():
    d.intent("android.settings.SETTINGS")
    d.wait_idle()


# Droidline ---------------------------------------------------------------
settings_home()
record("droidline", "read screen tree", [timed(lambda: d.dump()) for _ in range(N)])
record("droidline", "find by text", [timed(lambda: d.find("text", LABEL)) for _ in range(N)])
taps = []
for _ in range(N):
    taps.append(timed(lambda: d.touch("text", LABEL)))
    d.wait_idle()
    d.back()
    d.wait_idle()
record("droidline", "find and tap", taps)
record("droidline", "screenshot", [timed(lambda: d.screenshot()) for _ in range(N)])

# adb + uiautomator -------------------------------------------------------
def adb(*args, binary=False):
    r = subprocess.run(["adb", "-s", serial, *args], capture_output=True, timeout=60)
    return r.stdout if binary else r.stdout.decode("utf-8", "replace")


def adb_dump():
    adb("shell", "uiautomator", "dump", "/sdcard/dl_bench.xml")
    return adb("shell", "cat", "/sdcard/dl_bench.xml")


def adb_find():
    xml = adb_dump()
    m = re.search(r'text="%s"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"' % LABEL, xml)
    if not m:
        raise RuntimeError("label not in adb dump")
    l, t, r, b = map(int, m.groups())
    return (l + r) // 2, (t + b) // 2


def adb_tap():
    x, y = adb_find()
    adb("shell", "input", "tap", str(x), str(y))


settings_home()
record("adb", "read screen tree", [timed(adb_dump) for _ in range(N)])
record("adb", "find by text", [timed(adb_find) for _ in range(N)])
taps = []
for _ in range(N):
    taps.append(timed(adb_tap))
    time.sleep(1.5)
    d.back()
    d.wait_idle()
record("adb", "find and tap", taps)
record("adb", "screenshot", [timed(lambda: adb("exec-out", "screencap", "-p", binary=True)) for _ in range(N)])
adb("shell", "rm", "-f", "/sdcard/dl_bench.xml")
open(f"results-{name}-droidline-adb.json", "w", encoding="utf-8").write(json.dumps(out, ensure_ascii=False, indent=1))
