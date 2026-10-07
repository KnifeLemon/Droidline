from __future__ import annotations

from typing import Any, Dict, Optional


class DroidlineError(Exception):
    """Base class of every error the SDK raises.

    `code` is an error code from spec/commands.json (or a client-side code such as
    SERVER_NOT_RUNNING), `data` holds the extra fields the server sent, like `screen`
    or `candidates`. Those fields are also readable as attributes: `err.screen`.
    """

    code: str = "INTERNAL"
    http: Optional[int] = None
    retryable: bool = False

    def __init__(
        self,
        msg: str,
        code: Optional[str] = None,
        retryable: Optional[bool] = None,
        data: Optional[Dict[str, Any]] = None,
    ) -> None:
        super().__init__(msg)
        self.msg = msg
        if code is not None:
            self.code = code
        if retryable is not None:
            self.retryable = retryable
        self.data: Dict[str, Any] = dict(data or {})

    def __getattr__(self, name: str) -> Any:
        data = self.__dict__.get("data", {})
        if name.startswith("_") or name not in data:
            raise AttributeError(name)
        return data[name]

    def __repr__(self) -> str:
        return f"{type(self).__name__}(code={self.code!r}, msg={self.msg!r}, data={self.data!r})"


class ServerNotRunningError(DroidlineError):
    """Nothing is listening on the client port. Start the server with `droidline serve`."""

    code = "SERVER_NOT_RUNNING"


class ConnectionLostError(DroidlineError):
    """The server connection dropped while a call was in flight. The command may or may not have run."""

    code = "CONNECTION_LOST"
