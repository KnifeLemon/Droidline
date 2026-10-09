"""Optional pytest plugin. Nothing loads it by itself; turn it on with

    pytest -p droidline.pytest_plugin

or `pytest_plugins = ["droidline.pytest_plugin"]` in conftest.py. It adds a `phone`
fixture and, when a test fails, the phone's recent commands to the report. Pass
--droidline-artifacts DIR to also save a screenshot and the screen tree of each failure.
"""

from __future__ import annotations

import os
import re
from typing import Any, Iterator, Optional

import pytest

from ._client import Device, Droidline, LeasedDevice
from .testing import format_history, save_failure


def pytest_addoption(parser: Any) -> None:
    group = parser.getgroup("droidline")
    group.addoption("--droidline-device", default=None, help="phone ID or name for the phone fixture")
    group.addoption("--droidline-lease", action="store_true", default=False,
                    help="lease a free phone for the session, so parallel workers get different phones")
    group.addoption("--droidline-artifacts", default=None, metavar="DIR",
                    help="save a screenshot and the screen tree of each failing test here")


@pytest.fixture(scope="session")
def droidline_client() -> Iterator[Droidline]:
    """One connection to the server for the whole session."""
    client = Droidline()
    yield client
    client.close()


@pytest.fixture(scope="session")
def _droidline_session_phone(request: Any, droidline_client: Droidline) -> Iterator[Device]:
    device = request.config.getoption("--droidline-device")
    if request.config.getoption("--droidline-lease"):
        leased: LeasedDevice = droidline_client.lease(device)
        yield leased
        leased.release()
    else:
        yield droidline_client.device(device)


@pytest.fixture
def phone(_droidline_session_phone: Device) -> Device:
    """The phone to test on: --droidline-device, else DROIDLINE_DEVICE, else the only online phone."""
    return _droidline_session_phone


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item: Any, call: Any) -> Iterator[None]:
    outcome = yield
    report = outcome.get_result()
    if report.when != "call" or not report.failed:
        return
    device: Optional[Device] = item.funcargs.get("phone") if hasattr(item, "funcargs") else None
    if device is None:
        return
    history = device.history[-30:]
    if history:
        report.sections.append(("droidline commands", format_history(history)))
    folder = item.config.getoption("--droidline-artifacts")
    if folder:
        safe = re.sub(r"[^A-Za-z0-9_.-]+", "_", item.nodeid).strip("_")
        saved = save_failure(device, os.path.join(folder, safe))
        report.sections.append(("droidline artifacts", "\n".join(saved)))
