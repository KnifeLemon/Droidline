"""Control Android phones through a local Droidline server, without ADB.

    from droidline import connect

    d = connect()
    d.touch("id", "com.kakao.talk:id/login")

Docs: https://droidline.dev/docs
"""

from . import _generated
from ._client import Device, Droidline, LeasedDevice, connect, lease
from ._element import Element
from ._errors import ConnectionLostError, DroidlineError, ServerNotRunningError
from ._generated import *  # noqa: F401,F403

__version__ = "0.1.3"

__all__ = [
    "connect",
    "lease",
    "Droidline",
    "Device",
    "LeasedDevice",
    "Element",
    "DroidlineError",
    "ServerNotRunningError",
    "ConnectionLostError",
]
__all__ += _generated.__all__
