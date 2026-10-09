from __future__ import annotations

import json
import threading
import time

import pytest
from conftest import PNG_BYTES, FakeServer, free_port, wait_until

from droidline import (
    ConnectionLostError,
    DroidlineError,
    DroidlineTimeoutError,
    Droidline,
    NotFoundError,
    ServerNotRunningError,
    UnauthorizedError,
    connect,
)


def test_sends_only_the_params_the_caller_passed(server: FakeServer) -> None:
    d = connect(port=server.port)
    d.touch("id", "com.kakao.talk:id/login")
    assert server.sent_params("touch") == {"cmd": "touch", "by": "id", "value": "com.kakao.talk:id/login"}

    d.touch("text", "확인", 1, timeout=5)
    assert server.sent_params("touch") == {"cmd": "touch", "by": "text", "value": "확인", "nth": 1, "timeout": 5}


def test_device_field_and_return_kinds(server: FakeServer) -> None:
    d = connect("shelf-01", port=server.port)
    assert d.exists("text", "광고 닫기") is False
    assert d.touch("id", "login") == {"via": "node", "ms": 412}
    assert d.back() is None
    assert server.sent_params("back") == {"cmd": "back", "device": "shelf-01"}


def test_responses_are_matched_by_id_out_of_order(server: FakeServer) -> None:
    held = []

    def handler(conn, msg):
        if msg["cmd"] not in ("exists", "get_text"):
            return server.default(conn, msg)
        held.append((conn, msg))
        if len(held) == 2:
            for c, m in reversed(held):
                c.reply(m, value=True if m["cmd"] == "exists" else "잔액 1,000원")

    server.handler = handler
    client = Droidline(port=server.port)
    results = {}

    def run(name, fn):
        results[name] = fn()

    threads = [
        threading.Thread(target=run, args=("exists", lambda: client.device("a").exists("text", "x"))),
        threading.Thread(target=run, args=("text", lambda: client.device("b").get_text("id", "balance"))),
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join(5)
    assert results == {"exists": True, "text": "잔액 1,000원"}
    devices = {m["cmd"]: m["device"] for _, m in held}
    assert devices == {"exists": "a", "get_text": "b"}
    client.close()


def test_events_interleaved_with_responses(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] != "touch":
            return server.default(conn, msg)
        conn.send({"event": "screen", "device": "a1b2c3d4", "package": "com.kakao.talk"})
        # A late result for an older request carries an id too; it must not resolve this call.
        conn.send({"event": "result", "id": msg["id"], "ok": True, "results": []})
        conn.reply(msg, via="node", ms=5)
        conn.send({"event": "toast", "device": "a1b2c3d4", "text": "저장됨"})

    server.handler = handler
    client = Droidline(port=server.port)
    events = []
    client.on("*", events.append)
    screens = []
    client.on("screen", screens.append)

    assert client.device().touch("text", "저장") == {"via": "node", "ms": 5}
    wait_until(lambda: len(events) == 3)
    assert [e["event"] for e in events] == ["screen", "result", "toast"]
    assert screens == [events[0]]
    client.close()


def test_slow_event_callback_does_not_block_replies(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "home":
            conn.send({"event": "toast", "text": "slow"})
        server.default(conn, msg)

    server.handler = handler
    client = Droidline(port=server.port)
    started = threading.Event()

    def slow(event):
        started.set()
        time.sleep(1.0)

    client.on("toast", slow)
    d = client.device()
    d.home()
    assert started.wait(2)
    t0 = time.monotonic()
    d.back()
    assert time.monotonic() - t0 < 0.5
    client.close()


def test_error_reply_raises_mapped_exception_with_fields(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "input":
            conn.send({
                "id": msg["id"], "ok": False, "error": "NOT_FOUND",
                "msg": "10초 동안 text '아이디' 대상을 찾지 못했습니다. 현재 화면: com.kakao.talk / .LoginActivity",
                "screen": "com.kakao.talk/.LoginActivity", "retryable": True,
            })
        elif msg["cmd"] == "wait_gone":
            conn.send({"id": msg["id"], "ok": False, "error": "TIMEOUT", "msg": "wait_gone did not finish"})
        elif msg["cmd"] == "future":
            conn.send({"id": msg["id"], "ok": False, "error": "SOMETHING_NEW", "msg": "new", "extra": 1})
        else:
            server.default(conn, msg)

    server.handler = handler
    d = connect(port=server.port)
    with pytest.raises(NotFoundError) as info:
        d.input("text", "아이디", "knife")
    err = info.value
    assert isinstance(err, DroidlineError)
    assert err.code == "NOT_FOUND"
    assert err.retryable is True
    assert err.data == {"screen": "com.kakao.talk/.LoginActivity"}
    assert err.screen == "com.kakao.talk/.LoginActivity"
    assert "아이디" in str(err)

    with pytest.raises(DroidlineTimeoutError) as info:
        d.wait_gone("text", "로딩 중")
    assert info.value.retryable is True

    with pytest.raises(DroidlineError) as info:
        d.call("future")
    assert type(info.value) is DroidlineError
    assert info.value.code == "SOMETHING_NEW"
    assert info.value.extra == 1


def test_screenshot_saves_to_path_or_returns_bytes(server: FakeServer, tmp_path) -> None:
    d = connect(port=server.port)
    path = str(tmp_path / "a.png")
    assert d.screenshot(path) == path
    with open(path, "rb") as f:
        assert f.read() == PNG_BYTES
    assert server.sent_params("screenshot") == {"cmd": "screenshot", "format": "png"}

    assert d.screenshot(str(tmp_path / "b.jpg"), quality=50) == str(tmp_path / "b.jpg")
    assert server.sent_params("screenshot") == {"cmd": "screenshot", "format": "jpeg", "quality": 50}

    assert d.screenshot() == PNG_BYTES
    assert server.sent_params("screenshot") == {"cmd": "screenshot"}


def test_dump_saves_json_and_returns_the_tree(server: FakeServer, tmp_path) -> None:
    d = connect(port=server.port)
    path = tmp_path / "screen.json"
    result = d.dump(str(path))
    assert result["tree"]["text"] == "로그인"
    assert "id" not in result and "ok" not in result
    assert json.loads(path.read_text(encoding="utf-8")) == result
    assert "로그인" in path.read_text(encoding="utf-8")
    assert server.sent_params("dump") == {"cmd": "dump"}

    assert d.dump(all_windows=True)["package"] == "com.kakao.talk"
    assert server.sent_params("dump") == {"cmd": "dump", "all_windows": True}


def test_selector_aliases_send_the_alias_as_cmd(server: FakeServer) -> None:
    d = connect(port=server.port)
    assert d.touchById("com.kakao.talk:id/login") == {"via": "node", "ms": 120}
    assert server.sent_params("touchById") == {"cmd": "touchById", "value": "com.kakao.talk:id/login"}
    d.touchByText("확인", nth=2)
    assert server.sent_params("touchByText") == {"cmd": "touchByText", "value": "확인", "nth": 2}


def test_chrome_namespace(server: FakeServer) -> None:
    d = connect(port=server.port)
    assert d.chrome.go("naver.com", new_tab=True) == {"ms": 900}
    assert server.sent_params("chrome.go") == {"cmd": "chrome.go", "url": "naver.com", "new_tab": True}


def test_which_and_batch_send_tuples_as_arrays(server: FakeServer) -> None:
    d = connect(port=server.port)
    assert d.which([("text", "로그인"), ("id", "x")], timeout=10) == 1
    assert server.sent_params("which") == {"cmd": "which", "candidates": [["text", "로그인"], ["id", "x"]], "timeout": 10}

    d.batch([("touch", "text", "Wi-Fi"), ("sleep", 3000), ("touch", "text", "Wi-Fi")])
    assert server.sent_params("batch") == {
        "cmd": "batch",
        "steps": [["touch", "text", "Wi-Fi"], ["sleep", 3000], ["touch", "text", "Wi-Fi"]],
    }


def test_wait_flag_on_network_cutting_batches(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] != "batch":
            return server.default(conn, msg)
        if msg.get("wait"):
            conn.reply(msg, results=[{"ok": True}])
        else:
            conn.reply(msg, accepted=True)
            conn.send({"event": "result", "id": msg["id"], "device": "a1b2c3d4", "ok": True, "results": [{"ok": True}]})

    server.handler = handler
    d = connect(port=server.port)
    late = []
    d.client.on("result", late.append)
    steps = [("touch", "text", "Wi-Fi"), ("sleep", 3000), ("touch", "text", "Wi-Fi")]

    assert d.batch(steps, cuts_network=True) == {"accepted": True}
    assert "wait" not in server.last("batch")
    wait_until(lambda: len(late) == 1)
    assert late[0]["results"] == [{"ok": True}]

    assert d.batch(steps, cuts_network=True, wait=True) == {"results": [{"ok": True}]}
    assert server.last("batch")["wait"] is True
    assert server.last("batch")["cuts_network"] is True


def test_token_is_sent_as_the_first_line(server: FakeServer) -> None:
    d = connect(port=server.port, token="dlc_test")
    d.back()
    first, second = server.received[:2]
    assert {k: v for k, v in first.items() if k != "id"} == {"cmd": "auth", "token": "dlc_test"}
    assert second["cmd"] == "back"


def test_rejected_token_raises_unauthorized(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "auth":
            conn.send({"id": msg["id"], "ok": False, "error": "UNAUTHORIZED", "msg": "This connection needs a client token"})
        else:
            server.default(conn, msg)

    server.handler = handler
    with pytest.raises(UnauthorizedError):
        connect(port=server.port, token="wrong")


def test_environment_variables(server: FakeServer, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("DROIDLINE_PORT", str(server.port))
    monkeypatch.setenv("DROIDLINE_TOKEN", "dlc_env")
    monkeypatch.setenv("DROIDLINE_DEVICE", "shelf-02")
    d = connect()
    d.home()
    assert server.received[0]["token"] == "dlc_env"
    assert server.last("home")["device"] == "shelf-02"


def test_reconnects_after_the_server_drops_the_connection(server: FakeServer) -> None:
    drop_after_back = {"armed": True}

    def handler(conn, msg):
        server.default(conn, msg)
        if msg["cmd"] == "back" and drop_after_back["armed"]:
            drop_after_back["armed"] = False
            conn.close()

    server.handler = handler
    d = connect(port=server.port, token="dlc_test")
    d.client.subscribe(["device"])
    d.back()
    wait_until(lambda: not d.client.connected)

    d.home()
    assert len(server.connections) == 2
    replay = [{k: v for k, v in m.items() if k != "id"} for m in server.connections[1].received]
    assert replay == [
        {"cmd": "auth", "token": "dlc_test"},
        {"cmd": "subscribe", "events": ["device"]},
        {"cmd": "home"},
    ]


def test_call_in_flight_fails_when_the_connection_drops(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "launch":
            conn.close()
        else:
            server.default(conn, msg)

    server.handler = handler
    d = connect(port=server.port)
    with pytest.raises(ConnectionLostError):
        d.launch("com.kakao.talk")
    assert d.home() is None


def test_on_notification_filters_by_device_package_and_text(server: FakeServer) -> None:
    d = connect("shelf-01", port=server.port)
    got = []
    everything = []
    d.client.on("notification", everything.append)
    stop = d.on_notification(package="com.kakao.talk", textContains="3시", callback=got.append)
    d.on_notification(callback=lambda n: None)

    subscribes = [m for m in server.received if m["cmd"] == "subscribe"]
    assert [{k: v for k, v in m.items() if k != "id"} for m in subscribes] == [
        {"cmd": "subscribe", "events": ["notification"]}
    ]

    def note(device, package, title, text):
        return {"event": "notification", "device": device, "key": text, "package": package, "title": title, "text": text}

    conn = server.connections[0]
    for n in [
        note("a1b2c3d4", "com.kakao.talk", "홍길동", "내일 3시에 봬요"),
        note("zzzz9999", "com.kakao.talk", "홍길동", "3시 다른 폰"),
        note("a1b2c3d4", "com.android.chrome", "", "3시 다른 앱"),
        note("a1b2c3d4", "com.kakao.talk", "홍길동", "안녕"),
        note("a1b2c3d4", "com.kakao.talk", "3시 회의", "제목에만"),
    ]:
        conn.send(n)
    wait_until(lambda: len(everything) == 5)
    assert [n["key"] for n in got] == ["내일 3시에 봬요", "제목에만"]

    stop()
    conn.send(note("a1b2c3d4", "com.kakao.talk", "", "3시 again"))
    wait_until(lambda: len(everything) == 6)
    assert len(got) == 2


def test_server_level_and_device_level_wait_notification(server: FakeServer) -> None:
    client = Droidline(port=server.port)
    assert client.wait_notification("textContains", "인증번호", 60)["text"] == "인증번호 1234"
    assert server.sent_params("wait_notification") == {
        "cmd": "wait_notification", "by": "textContains", "value": "인증번호", "timeout": 60,
    }
    client.device("shelf-01").wait_notification("textContains", "인증번호")
    assert server.last("wait_notification")["device"] == "shelf-01"
    client.close()


def test_call_escape_hatch(server: FakeServer) -> None:
    d = connect("shelf-01", port=server.port)
    assert d.call("future_cmd", foo=1) == {}
    assert server.sent_params("future_cmd") == {"cmd": "future_cmd", "device": "shelf-01", "foo": 1}


def test_context_manager_closes_the_connection(server: FakeServer) -> None:
    with connect(port=server.port) as d:
        d.back()
        assert d.client.connected
    assert not d.client.connected


def test_server_not_running_names_droidline_serve() -> None:
    with pytest.raises(ServerNotRunningError) as info:
        connect(port=free_port())
    assert "droidline serve" in str(info.value)


NODE = {"text": "Wi-Fi", "id": "android:id/title", "desc": "", "class": "android.widget.TextView", "bounds": [40, 330, 400, 380], "checked": False}


def test_query_selectors_and_input_after_a_query(server: FakeServer) -> None:
    d = connect(port=server.port)
    d.touch({"text": "확인", "clickable": True})
    assert server.sent_params("touch") == {"cmd": "touch", "by": {"text": "확인", "clickable": True}}
    d.input({"editable": True}, "knife")
    assert server.sent_params("input") == {"cmd": "input", "by": {"editable": True}, "text": "knife"}
    d.input("id", "email", "knife")
    assert server.sent_params("input") == {"cmd": "input", "by": "id", "value": "email", "text": "knife"}


def test_find_returns_elements_that_act_on_themselves(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "find":
            return conn.reply(msg, value=NODE)
        if msg["cmd"] == "find_all":
            return conn.reply(msg, value=[NODE, dict(NODE, text="Bluetooth", bounds=[40, 530, 400, 580])])
        return server.default(conn, msg)

    server.handler = handler
    d = connect(port=server.port)
    el = d.find("text", "Wi-Fi")
    assert (el.text, el.id, el.class_name, el.bounds, el.center) == ("Wi-Fi", "android:id/title", "android.widget.TextView", (40, 330, 400, 380), (220, 355))
    assert el["checked"] is False
    el.click()
    assert server.sent_params("touch") == {"cmd": "touch", "by": {"bounds": [40, 330, 400, 380], "class": "android.widget.TextView"}, "timeout": 0}
    el.find("id", "summary")
    assert server.sent_params("find")["by"] == {"id": "summary", "inside": {"bounds": [40, 330, 400, 380], "class": "android.widget.TextView"}}
    assert [e.text for e in d.find_all("id", "title")] == ["Wi-Fi", "Bluetooth"]


def test_lease_carries_the_lease_and_releases(server: FakeServer) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "lease":
            return conn.reply(msg, lease="l-1", device="a1b2c3d4", name="shelf-01", ttl=300)
        return server.default(conn, msg)

    server.handler = handler
    from droidline import lease

    with lease(wait=5, port=server.port) as d:
        d.home()
        assert server.sent_params("home") == {"cmd": "home", "device": "a1b2c3d4", "lease": "l-1"}
    assert server.sent_params("release") == {"cmd": "release", "lease": "l-1"}
    assert server.sent_params("lease") == {"cmd": "lease", "wait": 5}


def test_history_and_image_files(server: FakeServer, tmp_path) -> None:
    def handler(conn, msg):
        if msg["cmd"] == "exists":
            return conn.send({"id": msg["id"], "ok": False, "error": "NOT_FOUND", "msg": "not there"})
        return server.default(conn, msg)

    server.handler = handler
    d = connect("shelf-01", port=server.port)
    d.home()
    with pytest.raises(NotFoundError):
        d.exists("text", "x")
    entries = d.history
    assert entries[-2]["cmd"] == "home" and entries[-2]["ok"] is True
    assert entries[-1]["ok"] is False and entries[-1]["error"] == "NOT_FOUND"

    pic = tmp_path / "button.png"
    pic.write_bytes(PNG_BYTES)
    d.find_image(str(pic))
    sent = server.sent_params("find_image")
    assert sent["image"] and sent["image"] != str(pic)
    d.find_image(PNG_BYTES)
    assert server.sent_params("find_image")["image"] == sent["image"]
