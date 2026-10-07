"""In-process fake of the Droidline client API (PROTOCOL.md section 3) for the SDK tests."""

from __future__ import annotations

import base64
import json
import socket
import threading
import time
from typing import Any, Callable, Dict, List

import pytest

PNG_BYTES = b"\x89PNG\r\n\x1a\nfake-image-bytes"

CANNED: Dict[str, Dict[str, Any]] = {
    "exists": {"value": False},
    "get_text": {"value": ""},
    "touch": {"via": "node", "ms": 412},
    "touchById": {"via": "node", "ms": 120},
    "touchByText": {"via": "gesture", "ms": 300},
    "which": {"value": 1},
    "batch": {"results": [{"ok": True}]},
    "chrome.go": {"ms": 900},
    "screenshot": {"format": "png", "width": 2, "height": 2, "data": base64.b64encode(PNG_BYTES).decode()},
    "dump": {
        "package": "com.kakao.talk",
        "activity": ".LoginActivity",
        "width": 1080,
        "height": 2340,
        "tree": {"text": "로그인", "id": "com.kakao.talk:id/login", "children": []},
    },
    "devices": {
        "value": [
            {"id": "a1b2c3d4", "name": "shelf-01", "online": True},
            {"id": "zzzz9999", "name": "shelf-02", "online": True},
        ]
    },
    "wait_notification": {"value": {"key": "k1", "package": "com.kakao.talk", "text": "인증번호 1234"}},
}


class FakeConn:
    def __init__(self, server: "FakeServer", sock: socket.socket) -> None:
        self.server = server
        self.sock = sock
        self.received: List[Dict[str, Any]] = []
        self._send_lock = threading.Lock()

    def serve(self) -> None:
        try:
            with self.sock.makefile("rb") as stream:
                for raw in stream:
                    msg = json.loads(raw)
                    with self.server.cond:
                        self.received.append(msg)
                        self.server.received.append(msg)
                        self.server.cond.notify_all()
                    self.server.handler(self, msg)
        except (OSError, ValueError):
            pass

    def send(self, obj: Dict[str, Any]) -> None:
        with self._send_lock:
            self.sock.sendall((json.dumps(obj, ensure_ascii=False) + "\n").encode("utf-8"))

    def reply(self, msg: Dict[str, Any], **fields: Any) -> None:
        self.send({"id": msg["id"], "ok": True, **fields})

    def close(self) -> None:
        try:
            self.sock.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        self.sock.close()


class FakeServer:
    def __init__(self) -> None:
        self.received: List[Dict[str, Any]] = []
        self.connections: List[FakeConn] = []
        self.cond = threading.Condition()
        self.handler: Callable[[FakeConn, Dict[str, Any]], None] = self.default
        self._sock = socket.create_server(("127.0.0.1", 0))
        self.port = self._sock.getsockname()[1]
        threading.Thread(target=self._accept, daemon=True).start()

    def _accept(self) -> None:
        while True:
            try:
                sock, _ = self._sock.accept()
            except OSError:
                return
            conn = FakeConn(self, sock)
            with self.cond:
                self.connections.append(conn)
                self.cond.notify_all()
            threading.Thread(target=conn.serve, daemon=True).start()

    def default(self, conn: FakeConn, msg: Dict[str, Any]) -> None:
        conn.reply(msg, **CANNED.get(msg["cmd"], {}))

    def wait_for(self, predicate: Callable[[], bool], timeout: float = 3.0) -> None:
        deadline = time.monotonic() + timeout
        with self.cond:
            while not predicate():
                left = deadline - time.monotonic()
                if left <= 0:
                    raise AssertionError("fake server: condition not met in time")
                self.cond.wait(left)

    def last(self, cmd: str) -> Dict[str, Any]:
        with self.cond:
            for msg in reversed(self.received):
                if msg.get("cmd") == cmd:
                    return msg
        raise AssertionError(f"fake server never received {cmd}")

    def sent_params(self, cmd: str) -> Dict[str, Any]:
        return {k: v for k, v in self.last(cmd).items() if k != "id"}

    def close(self) -> None:
        self._sock.close()
        for conn in list(self.connections):
            conn.close()


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch) -> None:
    for name in ("DROIDLINE_HOST", "DROIDLINE_PORT", "DROIDLINE_TOKEN", "DROIDLINE_DEVICE"):
        monkeypatch.delenv(name, raising=False)


@pytest.fixture
def server():
    srv = FakeServer()
    yield srv
    srv.close()


def wait_until(predicate: Callable[[], bool], timeout: float = 3.0) -> None:
    deadline = time.monotonic() + timeout
    while not predicate():
        if time.monotonic() > deadline:
            raise AssertionError("condition not met in time")
        time.sleep(0.01)


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]

