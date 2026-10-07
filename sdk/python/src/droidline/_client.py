"""Connection to the Droidline PC server over its client API (spec/PROTOCOL.md section 3)."""

from __future__ import annotations

import base64
import itertools
import json
import os
import queue
import socket
import threading
import traceback
from typing import Any, Callable, Dict, List, Optional, Set

from ._errors import ConnectionLostError, DroidlineError, ServerNotRunningError
from ._generated import ERRORS, ClientCommands, DeviceCommands, Notification

DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8780

EventCallback = Callable[[Dict[str, Any]], Any]


def _error_from(resp: Dict[str, Any]) -> DroidlineError:
    code = resp.get("error") or "INTERNAL"
    cls = ERRORS.get(code, DroidlineError)
    data = {k: v for k, v in resp.items() if k not in ("id", "ok", "error", "msg", "retryable")}
    retryable = resp.get("retryable")
    if not isinstance(retryable, bool):
        retryable = cls.retryable
    return cls(resp.get("msg") or code, code=code, retryable=retryable, data=data)


def _fields(resp: Dict[str, Any]) -> Dict[str, Any]:
    return {k: v for k, v in resp.items() if k not in ("id", "ok")}


def _message(cmd: str, device: Optional[str], required: Dict[str, Any], optional: Dict[str, Any]) -> Dict[str, Any]:
    msg: Dict[str, Any] = {"cmd": cmd}
    if device is not None:
        msg["device"] = device
    msg.update(required)
    msg.update((k, v) for k, v in optional.items() if v is not None)
    return msg


def _result(kind: str, resp: Dict[str, Any]) -> Any:
    if kind == "value":
        return resp.get("value")
    if kind == "fields":
        return _fields(resp)
    return None


class _Slot:
    __slots__ = ("done", "resp", "error")

    def __init__(self) -> None:
        self.done = threading.Event()
        self.resp: Optional[Dict[str, Any]] = None
        self.error: Optional[BaseException] = None


class _Link:
    """One TCP connection and the requests still waiting for a reply on it."""

    def __init__(self, sock: socket.socket) -> None:
        self.sock = sock
        self.alive = True
        self.pending: Dict[Any, _Slot] = {}


class _Connection:
    """Pipelines NDJSON requests over one socket and matches replies by id.

    A reader thread per socket routes replies to waiting callers and hands events to a
    separate dispatcher thread, so a slow event callback never delays a reply.
    """

    def __init__(self, host: str, port: int, token: Optional[str]) -> None:
        self.host = host
        self.port = port
        self.token = token
        self._lock = threading.Lock()
        self._connect_lock = threading.Lock()
        self._send_lock = threading.Lock()
        self._ids = itertools.count(1)
        self._link: Optional[_Link] = None
        self._subscriptions: List[Dict[str, Any]] = []
        self._listeners: Dict[str, List[EventCallback]] = {}
        self._queue: Optional["queue.Queue[Optional[Dict[str, Any]]]"] = None

    def request(self, msg: Dict[str, Any]) -> Dict[str, Any]:
        resp = self._exchange(self.ensure(), msg)
        if msg.get("cmd") == "subscribe":
            with self._lock:
                if msg not in self._subscriptions:
                    self._subscriptions.append(dict(msg))
        elif msg.get("cmd") == "auth":
            self.token = msg.get("token")
        return resp

    def subscribe_once(self, msg: Dict[str, Any]) -> None:
        with self._lock:
            known = msg in self._subscriptions
        if not known:
            self.request(msg)

    def ensure(self) -> _Link:
        with self._connect_lock:
            link = self._link
            if link is not None and link.alive:
                return link
            link = self._open()
            try:
                # Auth must be the first line, and subscriptions belong to the socket, so a
                # reconnect replays both before any other request goes out.
                if self.token:
                    self._exchange(link, {"cmd": "auth", "token": self.token})
                with self._lock:
                    subscriptions = list(self._subscriptions)
                for sub in subscriptions:
                    self._exchange(link, sub)
            except BaseException:
                self._drop(link)
                raise
            self._link = link
            return link

    def _open(self) -> _Link:
        try:
            sock = socket.create_connection((self.host, self.port), timeout=5)
        except OSError as e:
            raise ServerNotRunningError(
                f"Cannot reach the Droidline server at {self.host}:{self.port} ({e.strerror or e}). "
                "Start it with `droidline serve`."
            ) from None
        sock.settimeout(None)
        sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        link = _Link(sock)
        threading.Thread(target=self._read_loop, args=(link,), name="droidline-reader", daemon=True).start()
        return link

    def _exchange(self, link: _Link, msg: Dict[str, Any]) -> Dict[str, Any]:
        slot = _Slot()
        with self._lock:
            if not link.alive:
                raise ConnectionLostError("The connection to the Droidline server dropped. Retry the call.")
            msg_id = next(self._ids)
            link.pending[msg_id] = slot
        line = json.dumps({"id": msg_id, **msg}, ensure_ascii=False, separators=(",", ":")) + "\n"
        try:
            with self._send_lock:
                link.sock.sendall(line.encode("utf-8"))
        except OSError:
            self._drop(link)
        # Short waits keep Ctrl+C working on Windows, where an untimed wait cannot be interrupted.
        while not slot.done.wait(0.5):
            pass
        if slot.error is not None:
            raise slot.error
        resp = slot.resp or {}
        if resp.get("ok") is False:
            raise _error_from(resp)
        return resp

    def _read_loop(self, link: _Link) -> None:
        try:
            with link.sock.makefile("rb") as stream:
                for raw in stream:
                    try:
                        msg = json.loads(raw)
                    except ValueError:
                        continue
                    if not isinstance(msg, dict):
                        continue
                    # Late results carry the request id too, so the event check comes first.
                    if "event" in msg:
                        self._emit(msg)
                        continue
                    msg_id = msg.get("id")
                    if not isinstance(msg_id, (int, str)):
                        continue
                    with self._lock:
                        slot = link.pending.pop(msg_id, None)
                    if slot is not None:
                        slot.resp = msg
                        slot.done.set()
        except (OSError, ValueError):
            pass
        finally:
            self._drop(link)

    def _drop(self, link: _Link) -> None:
        with self._lock:
            was_alive = link.alive
            link.alive = False
            pending = list(link.pending.values())
            link.pending.clear()
            if self._link is link:
                self._link = None
        if was_alive:
            # shutdown() wakes a reader blocked in recv(); close() alone does not on Linux.
            try:
                link.sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            link.sock.close()
        for slot in pending:
            slot.error = ConnectionLostError(
                "The connection to the Droidline server dropped before the reply arrived. "
                "The command may or may not have run."
            )
            slot.done.set()

    def add_listener(self, kind: str, callback: EventCallback) -> None:
        with self._lock:
            self._listeners.setdefault(kind, []).append(callback)

    def remove_listener(self, kind: str, callback: EventCallback) -> None:
        with self._lock:
            callbacks = self._listeners.get(kind, [])
            if callback in callbacks:
                callbacks.remove(callback)

    def _emit(self, event: Dict[str, Any]) -> None:
        with self._lock:
            if self._queue is None:
                self._queue = queue.Queue()
                threading.Thread(
                    target=self._dispatch_loop, args=(self._queue,), name="droidline-events", daemon=True
                ).start()
            q = self._queue
        q.put(event)

    def _dispatch_loop(self, q: "queue.Queue[Optional[Dict[str, Any]]]") -> None:
        while True:
            event = q.get()
            if event is None:
                return
            with self._lock:
                callbacks = list(self._listeners.get(str(event.get("event")), ())) + list(self._listeners.get("*", ()))
            for callback in callbacks:
                try:
                    callback(event)
                except Exception:
                    traceback.print_exc()

    @property
    def connected(self) -> bool:
        link = self._link
        return link is not None and link.alive

    def close(self) -> None:
        with self._lock:
            q, self._queue = self._queue, None
            link = self._link
        if q is not None:
            q.put(None)
        if link is not None:
            self._drop(link)


class Droidline(ClientCommands):
    """Client for the local Droidline server. Commands for one phone go through `device()`.

    Arguments fall back to DROIDLINE_HOST, DROIDLINE_PORT and DROIDLINE_TOKEN, then to
    127.0.0.1:8780 without a token. The socket opens on the first call and reopens after a drop.
    """

    def __init__(self, host: Optional[str] = None, port: Optional[int] = None, token: Optional[str] = None) -> None:
        self.host = host or os.environ.get("DROIDLINE_HOST") or DEFAULT_HOST
        self.port = int(port or os.environ.get("DROIDLINE_PORT") or DEFAULT_PORT)
        self._conn = _Connection(self.host, self.port, token or os.environ.get("DROIDLINE_TOKEN") or None)

    @property
    def connected(self) -> bool:
        """True while the socket to the server is open."""
        return self._conn.connected

    def device(self, id_or_name: Optional[str] = None) -> Device:
        """A handle for one phone. Without an argument it uses DROIDLINE_DEVICE, else the only online phone."""
        return Device(self, id_or_name or os.environ.get("DROIDLINE_DEVICE") or None)

    def call(self, cmd: str, /, **params: Any) -> Dict[str, Any]:
        """Send any command, including ones newer than this SDK. Returns the reply without id and ok."""
        return _fields(self._conn.request({"cmd": cmd, **params}))

    def on(self, kind: str, callback: EventCallback) -> EventCallback:
        """Call `callback(event)` for each event of this kind (device, notification, screen, toast,
        result) or every event with "*". Only result events arrive without subscribe()."""
        self._conn.add_listener(kind, callback)
        return callback

    def off(self, kind: str, callback: EventCallback) -> None:
        self._conn.remove_listener(kind, callback)

    def close(self) -> None:
        self._conn.close()

    def __enter__(self) -> Droidline:
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def __repr__(self) -> str:
        return f"Droidline({self.host!r}, {self.port!r})"

    def _run(self, cmd: str, kind: str, required: Dict[str, Any], /, **optional: Any) -> Any:
        return _result(kind, self._conn.request(_message(cmd, None, required, optional)))


class Device(DeviceCommands):
    """One phone. Every generated command method sends a request with this device's ID or name."""

    def __init__(self, client: Droidline, device: Optional[str] = None) -> None:
        self.client = client
        self.device = device
        self._owns_client = False

    def call(self, cmd: str, /, **params: Any) -> Dict[str, Any]:
        """Send any command to this phone, including ones newer than this SDK."""
        return _fields(self.client._conn.request(_message(cmd, self.device, params, {})))

    def on_notification(
        self,
        package: Optional[str] = None,
        textContains: Optional[str] = None,
        callback: Optional[Callable[[Notification], Any]] = None,
    ) -> Callable[[], None]:
        """Call `callback(notification)` for every matching notification from this phone.

        Args:
            package: Only from this app.
            textContains: Only if title or text contains this.
            callback: Called with the notification event on the SDK's event thread.

        The phone forwards only the apps allowed with notify_filter(). Returns a function
        that stops the callback.
        """
        if callback is None:
            raise TypeError("on_notification() needs callback=")
        ids = self._identities()

        def handler(event: Dict[str, Any]) -> None:
            if ids and event.get("device") not in ids and event.get("name") not in ids:
                return
            if package is not None and event.get("package") != package:
                return
            if textContains is not None and not any(
                textContains in str(event.get(k) or "") for k in ("title", "text")
            ):
                return
            callback(event)  # type: ignore[arg-type]

        conn = self.client._conn
        conn.add_listener("notification", handler)
        try:
            conn.subscribe_once({"cmd": "subscribe", "events": ["notification"]})
        except BaseException:
            conn.remove_listener("notification", handler)
            raise
        return lambda: conn.remove_listener("notification", handler)

    def _identities(self) -> Set[str]:
        # Events name the device by ID; resolve a name so both match. Empty means any device.
        if self.device is None:
            return set()
        ids = {self.device}
        try:
            for info in self.client.devices():
                if self.device in (info.get("id"), info.get("name")):
                    ids.update(str(v) for v in (info.get("id"), info.get("name")) if v)
        except DroidlineError:
            pass
        return ids

    def close(self) -> None:
        """Close the connection if connect() opened it for this device; otherwise do nothing."""
        if self._owns_client:
            self.client.close()

    def __enter__(self) -> Device:
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def __repr__(self) -> str:
        return f"Device({self.device!r})"

    def _run(self, cmd: str, kind: str, required: Dict[str, Any], /, **optional: Any) -> Any:
        return _result(kind, self.client._conn.request(_message(cmd, self.device, required, optional)))

    def _save_image(self, cmd: str, path: Optional[str], required: Dict[str, Any], /, **optional: Any) -> Any:
        if path is not None and optional.get("format") is None:
            ext = os.path.splitext(path)[1].lower()
            optional["format"] = {".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg"}.get(ext)
        result = self._run(cmd, "fields", required, **optional)
        image = base64.b64decode(result["data"])
        if path is None:
            return image
        with open(path, "wb") as f:
            f.write(image)
        return path

    def _save_json(self, cmd: str, path: Optional[str], required: Dict[str, Any], /, **optional: Any) -> Any:
        result = self._run(cmd, "fields", required, **optional)
        if path is not None:
            with open(path, "w", encoding="utf-8") as f:
                json.dump(result, f, ensure_ascii=False, indent=2)
        return result


def connect(
    device: Optional[str] = None,
    host: Optional[str] = None,
    port: Optional[int] = None,
    token: Optional[str] = None,
) -> Device:
    """Connect to the local Droidline server and return a Device.

    `device` is a device ID or name; without it DROIDLINE_DEVICE is used, else the only online
    phone. Raises ServerNotRunningError right away if the server is not running.
    """
    client = Droidline(host, port, token)
    client._conn.ensure()
    d = client.device(device)
    d._owns_client = True
    return d
