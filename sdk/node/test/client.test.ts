import assert from "node:assert/strict";
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, test } from "node:test";
import { connect, Droidline, DroidlineError, type Notification } from "../src/index.js";
import { FakeServer, PNG_BYTES, freePort, waitUntil, withoutId, type FakeConn, type Msg } from "./fake-server.js";

let server: FakeServer;
const ENV = ["DROIDLINE_HOST", "DROIDLINE_PORT", "DROIDLINE_TOKEN", "DROIDLINE_DEVICE"];
const closers: Array<() => void> = [];

beforeEach(async () => {
  for (const name of ENV) delete process.env[name];
  server = await FakeServer.start();
});

afterEach(async () => {
  for (const close of closers.splice(0)) close();
  for (const name of ENV) delete process.env[name];
  await server.close();
});

async function open(options: Parameters<typeof connect>[0] = {}) {
  const opts = typeof options === "string" ? { device: options } : options;
  const d = await connect({ port: server.port, ...opts });
  closers.push(() => d.close());
  return d;
}

function client(): Droidline {
  const c = new Droidline({ port: server.port });
  closers.push(() => c.close());
  return c;
}

test("sends only the params the caller passed", async () => {
  const d = await open();
  await d.touch("id", "com.kakao.talk:id/login");
  assert.deepEqual(server.sentParams("touch"), { cmd: "touch", by: "id", value: "com.kakao.talk:id/login" });

  await d.touch("text", "확인", 1, { timeout: 5 });
  assert.deepEqual(server.sentParams("touch"), { cmd: "touch", by: "text", value: "확인", nth: 1, timeout: 5 });

  await d.swipe(540, 1600, 540, 400, 300);
  assert.deepEqual(server.sentParams("swipe"), { cmd: "swipe", x1: 540, y1: 1600, x2: 540, y2: 400, ms: 300 });

  await d.sendkey("enter");
  assert.deepEqual(server.sentParams("sendkey"), { cmd: "sendkey", key: "enter" });

  await assert.rejects(() => (d.tap as any)(1, 2, 3), TypeError);
});

test("device field and return kinds", async () => {
  const d = await open("shelf-01");
  assert.equal(await d.exists("text", "광고 닫기"), false);
  assert.deepEqual(await d.touch("id", "login"), { via: "node", ms: 412 });
  assert.equal(await d.back(), undefined);
  assert.deepEqual(server.sentParams("back"), { cmd: "back", device: "shelf-01" });
});

test("responses are matched by id when they arrive out of order", async () => {
  const held: Array<[FakeConn, Msg]> = [];
  server.handler = (conn, msg) => {
    if (msg.cmd !== "exists" && msg.cmd !== "get_text") return server.default(conn, msg);
    held.push([conn, msg]);
    if (held.length === 2) {
      for (const [c, m] of [...held].reverse()) c.reply(m, { value: m.cmd === "exists" ? true : "잔액 1,000원" });
    }
  };
  const c = client();
  const [exists, text] = await Promise.all([c.device("a").exists("text", "x"), c.device("b").get_text("id", "balance")]);
  assert.equal(exists, true);
  assert.equal(text, "잔액 1,000원");
  assert.deepEqual(Object.fromEntries(held.map(([, m]) => [m.cmd, m.device])), { exists: "a", get_text: "b" });
});

test("events interleaved with responses", async () => {
  server.handler = (conn, msg) => {
    if (msg.cmd !== "touch") return server.default(conn, msg);
    conn.send({ event: "screen", device: "a1b2c3d4", package: "com.kakao.talk" });
    // A late result for an older request carries an id too; it must not resolve this call.
    conn.send({ event: "result", id: msg.id, ok: true, via: "settings_macro" });
    conn.reply(msg, { via: "node", ms: 5 });
    conn.send({ event: "toast", device: "a1b2c3d4", text: "저장됨" });
  };
  const c = client();
  const events: Msg[] = [];
  const screens: Msg[] = [];
  c.on("*", (e) => events.push(e));
  c.on("screen", (e) => screens.push(e));

  assert.deepEqual(await c.device().touch("text", "저장"), { via: "node", ms: 5 });
  await waitUntil(() => events.length === 3);
  assert.deepEqual(events.map((e) => e.event), ["screen", "result", "toast"]);
  assert.deepEqual(screens, [events[0]]);
});

test("a throwing event listener does not break replies", async () => {
  server.handler = (conn, msg) => {
    if (msg.cmd === "home") conn.send({ event: "toast", text: "boom" });
    server.default(conn, msg);
  };
  const c = client();
  const logged: unknown[] = [];
  const original = console.error;
  console.error = (err: unknown) => logged.push(err);
  try {
    c.on("toast", () => {
      throw new Error("listener bug");
    });
    const d = c.device();
    await d.home();
    await waitUntil(() => logged.length === 1);
    assert.equal(await d.back(), undefined);
  } finally {
    console.error = original;
  }
});

test("error reply rejects with DroidlineError carrying the fields", async () => {
  server.handler = (conn, msg) => {
    if (msg.cmd === "input") {
      conn.send({
        id: msg.id, ok: false, error: "NOT_FOUND",
        msg: "10초 동안 text '아이디' 대상을 찾지 못했습니다. 현재 화면: com.kakao.talk / .LoginActivity",
        screen: "com.kakao.talk/.LoginActivity", retryable: true,
      });
    } else if (msg.cmd === "wait_gone") {
      conn.send({ id: msg.id, ok: false, error: "TIMEOUT", msg: "wait_gone did not finish" });
    } else if (msg.cmd === "future") {
      conn.send({ id: msg.id, ok: false, error: "SOMETHING_NEW", msg: "new", extra: 1 });
    } else server.default(conn, msg);
  };
  const d = await open();
  const err = await d.input("text", "아이디", "knife").then(
    () => assert.fail("expected a rejection"),
    (e: unknown) => e,
  );
  assert.ok(err instanceof DroidlineError);
  assert.equal(err.code, "NOT_FOUND");
  assert.equal(err.retryable, true);
  assert.deepEqual(err.data, { screen: "com.kakao.talk/.LoginActivity" });
  assert.match(err.message, /아이디/);

  await assert.rejects(d.wait_gone("text", "로딩 중"), (e: DroidlineError) => e.code === "TIMEOUT" && e.retryable);
  await assert.rejects(d.call("future"), (e: DroidlineError) => e.code === "SOMETHING_NEW" && e.data.extra === 1);
});

test("screenshot saves to a path or returns a Buffer", async () => {
  const d = await open();
  const dir = await mkdtemp(join(tmpdir(), "droidline-"));
  const png = join(dir, "a.png");
  assert.equal(await d.screenshot(png), png);
  assert.deepEqual(await readFile(png), PNG_BYTES);
  assert.deepEqual(server.sentParams("screenshot"), { cmd: "screenshot", format: "png" });

  const jpg = join(dir, "b.jpg");
  assert.equal(await d.screenshot(jpg, { quality: 50 }), jpg);
  assert.deepEqual(server.sentParams("screenshot"), { cmd: "screenshot", format: "jpeg", quality: 50 });

  const image = await d.screenshot();
  assert.ok(Buffer.isBuffer(image));
  assert.deepEqual(image, PNG_BYTES);
  assert.deepEqual(server.sentParams("screenshot"), { cmd: "screenshot" });
});

test("dump saves JSON and returns the tree", async () => {
  const d = await open();
  const dir = await mkdtemp(join(tmpdir(), "droidline-"));
  const path = join(dir, "screen.json");
  const result = await d.dump(path);
  assert.equal(result.tree.text, "로그인");
  assert.ok(!("id" in result) && !("ok" in result));
  const text = await readFile(path, "utf8");
  assert.deepEqual(JSON.parse(text), result);
  assert.match(text, /로그인/);
  assert.deepEqual(server.sentParams("dump"), { cmd: "dump" });

  assert.equal((await d.dump({ all_windows: true })).package, "com.kakao.talk");
  assert.deepEqual(server.sentParams("dump"), { cmd: "dump", all_windows: true });
});

test("selector shorthands send the alias, camelCase names send the snake_case cmd", async () => {
  const d = await open();
  assert.deepEqual(await d.touchById("com.kakao.talk:id/login"), { via: "node", ms: 120 });
  assert.deepEqual(server.sentParams("touchById"), { cmd: "touchById", value: "com.kakao.talk:id/login" });
  await d.touchByText("확인", { nth: 2 });
  assert.deepEqual(server.sentParams("touchByText"), { cmd: "touchByText", value: "확인", nth: 2 });

  await d.longTap(540, 1200, 800);
  assert.deepEqual(server.sentParams("long_tap"), { cmd: "long_tap", x: 540, y: 1200, ms: 800 });
  await d.waitGone("text", "로딩 중", { timeout: 3 });
  assert.deepEqual(server.sentParams("wait_gone"), { cmd: "wait_gone", by: "text", value: "로딩 중", timeout: 3 });
});

test("chrome namespace", async () => {
  const d = await open();
  assert.deepEqual(await d.chrome.go("naver.com", { new_tab: true }), { ms: 900 });
  assert.deepEqual(server.sentParams("chrome.go"), { cmd: "chrome.go", url: "naver.com", new_tab: true });
});

test("which and batch send pairs and steps as arrays", async () => {
  const d = await open();
  assert.equal(await d.which([["text", "로그인"], ["id", "x"]], { timeout: 10 }), 1);
  assert.deepEqual(server.sentParams("which"), { cmd: "which", candidates: [["text", "로그인"], ["id", "x"]], timeout: 10 });

  await d.batch([["airplane", true], ["sleep", 3000], ["airplane", false]]);
  assert.deepEqual(server.sentParams("batch"), {
    cmd: "batch",
    steps: [["airplane", true], ["sleep", 3000], ["airplane", false]],
  });
});

test("wait flag on network-cutting commands", async () => {
  server.handler = (conn, msg) => {
    if (msg.cmd !== "airplane") return server.default(conn, msg);
    if (msg.wait) conn.reply(msg, { via: "settings_macro", ms: 2300 });
    else {
      conn.reply(msg, { accepted: true });
      conn.send({ event: "result", id: msg.id, device: "a1b2c3d4", ok: true, via: "settings_macro" });
    }
  };
  const d = await open();
  const late: Msg[] = [];
  d.client.on("result", (e) => late.push(e));

  assert.deepEqual(await d.airplane(true), { accepted: true });
  assert.ok(!("wait" in server.last("airplane")));
  await waitUntil(() => late.length === 1);
  assert.equal(late[0].via, "settings_macro");

  assert.deepEqual(await d.airplane(false, { wait: true }), { via: "settings_macro", ms: 2300 });
  assert.deepEqual(server.sentParams("airplane"), { cmd: "airplane", on: false, wait: true });

  await d.batch([["airplane", true], ["airplane", false]], { wait: true });
  assert.equal(server.last("batch").wait, true);
});

test("token is sent as the first line", async () => {
  const d = await open({ token: "dlc_test" });
  await d.back();
  assert.deepEqual(withoutId(server.received.slice(0, 2)), [{ cmd: "auth", token: "dlc_test" }, { cmd: "back" }]);
});

test("a rejected token rejects connect with UNAUTHORIZED", async () => {
  server.handler = (conn, msg) => {
    if (msg.cmd === "auth") conn.send({ id: msg.id, ok: false, error: "UNAUTHORIZED", msg: "This connection needs a client token" });
    else server.default(conn, msg);
  };
  await assert.rejects(connect({ port: server.port, token: "wrong" }), (e: DroidlineError) => e.code === "UNAUTHORIZED");
});

test("environment variables", async () => {
  process.env.DROIDLINE_PORT = String(server.port);
  process.env.DROIDLINE_TOKEN = "dlc_env";
  process.env.DROIDLINE_DEVICE = "shelf-02";
  const d = await connect();
  closers.push(() => d.close());
  await d.home();
  assert.equal(server.received[0].token, "dlc_env");
  assert.equal(server.last("home").device, "shelf-02");
});

test("reconnects after the server drops the connection", async () => {
  let armed = true;
  server.handler = (conn, msg) => {
    server.default(conn, msg);
    if (msg.cmd === "back" && armed) {
      armed = false;
      conn.close();
    }
  };
  const d = await open({ token: "dlc_test" });
  await d.client.subscribe(["device"]);
  await d.back();
  await waitUntil(() => !d.client.connected);

  await d.home();
  assert.equal(server.connections.length, 2);
  assert.deepEqual(withoutId(server.connections[1].received), [
    { cmd: "auth", token: "dlc_test" },
    { cmd: "subscribe", events: ["device"] },
    { cmd: "home" },
  ]);
});

test("a call in flight fails with CONNECTION_LOST when the connection drops", async () => {
  server.handler = (conn, msg) => {
    if (msg.cmd === "kill") conn.close();
    else server.default(conn, msg);
  };
  const d = await open();
  await assert.rejects(d.kill("com.kakao.talk"), (e: DroidlineError) => e.code === "CONNECTION_LOST");
  assert.equal(await d.home(), undefined);
});

test("onNotification filters by device, package and text", async () => {
  const d = await open("shelf-01");
  const got: Notification[] = [];
  const everything: Msg[] = [];
  d.client.on("notification", (n) => everything.push(n));
  const stop = await d.onNotification({ package: "com.kakao.talk", textContains: "3시" }, (n) => got.push(n));
  const stopOther = await d.on_notification(() => {});
  stopOther();

  assert.deepEqual(withoutId(server.received.filter((m) => m.cmd === "subscribe")), [
    { cmd: "subscribe", events: ["notification"] },
  ]);

  const note = (device: string, pkg: string, title: string, text: string) =>
    ({ event: "notification", device, key: text, package: pkg, title, text });
  const conn = server.connections[0];
  for (const n of [
    note("a1b2c3d4", "com.kakao.talk", "홍길동", "내일 3시에 봬요"),
    note("zzzz9999", "com.kakao.talk", "홍길동", "3시 다른 폰"),
    note("a1b2c3d4", "com.android.chrome", "", "3시 다른 앱"),
    note("a1b2c3d4", "com.kakao.talk", "홍길동", "안녕"),
    note("a1b2c3d4", "com.kakao.talk", "3시 회의", "제목에만"),
  ]) conn.send(n);
  await waitUntil(() => everything.length === 5);
  assert.deepEqual(got.map((n) => n.key), ["내일 3시에 봬요", "제목에만"]);

  stop();
  conn.send(note("a1b2c3d4", "com.kakao.talk", "", "3시 again"));
  await waitUntil(() => everything.length === 6);
  assert.equal(got.length, 2);
});

test("server-level and device-level wait_notification", async () => {
  const c = client();
  assert.equal((await c.wait_notification("textContains", "인증번호", 60)).text, "인증번호 1234");
  assert.deepEqual(server.sentParams("wait_notification"), {
    cmd: "wait_notification", by: "textContains", value: "인증번호", timeout: 60,
  });
  await c.device("shelf-01").waitNotification("textContains", "인증번호");
  assert.equal(server.last("wait_notification").device, "shelf-01");
});

test("call escape hatch", async () => {
  const d = await open("shelf-01");
  assert.deepEqual(await d.call("future_cmd", { foo: 1 }), {});
  assert.deepEqual(server.sentParams("future_cmd"), { cmd: "future_cmd", device: "shelf-01", foo: 1 });
});

test("close() on a connect() device closes the connection", async () => {
  const d = await open();
  await d.back();
  assert.equal(d.client.connected, true);
  d.close();
  assert.equal(d.client.connected, false);
});

test("server not running names droidline serve", async () => {
  const port = await freePort();
  await assert.rejects(connect({ port }), (e: DroidlineError) => e.code === "SERVER_NOT_RUNNING" && /droidline serve/.test(e.message));
});
