"""Control Android phones through a local Droidline server, without ADB.

    from droidline import connect

    d = connect()
    d.touch("id", "com.kakao.talk:id/login")

Docs: https://droidline.dev/docs
"""

from . import _generated
from ._client import Device, Droidline, connect
from ._errors import ConnectionLostError, DroidlineError, ServerNotRunningError
from ._generated import *  # noqa: F401,F403

__version__ = "0.1.0"

__all__ = [
    "connect",
    "Droidline",
    "Device",
    "DroidlineError",
    "ServerNotRunningError",
    "ConnectionLostError",
]
__all__ += _generated.__all__
