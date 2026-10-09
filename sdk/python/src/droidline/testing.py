"""Helpers for test suites, usable with any test runner:

    from droidline.testing import save_failure

    try:
        run_steps(d)
    except Exception:
        save_failure(d, "artifacts/login")
        raise
"""

from __future__ import annotations

import json
import os
import time
from typing import Any, Dict, List

from ._client import Device
from ._errors import DroidlineError


def format_history(entries: List[Dict[str, Any]]) -> str:
    """The command log as text, one line per request."""
    lines = []
    for e in entries:
        stamp = time.strftime("%H:%M:%S", time.localtime(e["time"]))
        result = "ok" if e["ok"] else e["error"]
        params = json.dumps(e["params"], ensure_ascii=False)
        lines.append(f"{stamp} {e['cmd']} {params} -> {result} ({e['ms']} ms)")
    return "\n".join(lines)


def save_failure(device: Device, folder: str) -> List[str]:
    """Save a screenshot, the screen tree and the command log of a phone into folder.

    Each part is best effort: a phone that is offline still gets its command log.
    """
    os.makedirs(folder, exist_ok=True)
    saved = []
    log = os.path.join(folder, "commands.txt")
    with open(log, "w", encoding="utf-8") as f:
        f.write(format_history(device.history) + "\n")
    saved.append(log)
    for name, take in (("screen.png", lambda p: device.screenshot(p, format="png")), ("screen.json", lambda p: device.dump(p))):
        path = os.path.join(folder, name)
        try:
            take(path)
            saved.append(path)
        except DroidlineError:
            pass
    return saved
