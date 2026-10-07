// In-process fake of the Droidline client API (PROTOCOL.md section 3) for the SDK tests.
import { createServer, type AddressInfo, type Server, type Socket } from "node:net";

export type Msg = Record<string, any>;

export const PNG_BYTES = Buffer.from("\x89PNG\r\n\x1a\nfake-image-bytes", "latin1");

export const CANNED: Record<string, Msg> = {
  exists: { value: false },
  get_text: { value: "" },
  touch: { via: "node", ms: 412 },
  touchById: { via: "node", ms: 120 },
  touchByText: { via: "gesture", ms: 300 },
  which: { value: 1 },
  batch: { results: [{ ok: true }] },
  "chrome.go": { ms: 900 },
  screenshot: { format: "png", width: 2, height: 2, data: PNG_BYTES.toString("base64") },
  dump: {
    package: "com.kakao.talk",
    activity: ".LoginActivity",
    width: 1080,
    height: 2340,
    tree: { text: "로그인", id: "com.kakao.talk:id/login", children: [] },
  },
  devices: {
    value: [
      { id: "a1b2c3d4", name: "shelf-01", online: true },
      { id: "zzzz9999", name: "shelf-02", online: true },
    ],
  },
  wait_notification: { value: { key: "k1", package: "com.kakao.talk", text: "인증번호 1234" } },
};

export class FakeConn {
  readonly received: Msg[] = [];

  constructor(readonly socket: Socket) {}

  send(obj: Msg): void {
    this.socket.write(JSON.stringify(obj) + "\n");
  }

  reply(msg: Msg, fields: Msg = {}): void {
    this.send({ id: msg.id, ok: true, ...fields });
  }

  close(): void {
    this.socket.end();
  }
}

export class FakeServer {
  readonly received: Msg[] = [];
  readonly connections: FakeConn[] = [];
  handler: (conn: FakeConn, msg: Msg) => void = (conn, msg) => this.default(conn, msg);
  port = 0;
  private readonly server: Server;

  private constructor() {
    this.server = createServer((socket) => {
      const conn = new FakeConn(socket);
      this.connections.push(conn);
      socket.setEncoding("utf8");
      socket.setNoDelay(true);
      socket.on("error", () => {});
      let buffer = "";
      socket.on("data", (chunk: string) => {
        buffer += chunk;
        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";
        for (const line of lines) {
          const msg = JSON.parse(line) as Msg;
          conn.received.push(msg);
          this.received.push(msg);
          this.handler(conn, msg);
        }
      });
    });
  }

  static async start(): Promise<FakeServer> {
    const fake = new FakeServer();
    await new Promise<void>((resolve) => fake.server.listen(0, "127.0.0.1", resolve));
    fake.port = (fake.server.address() as AddressInfo).port;
    return fake;
  }

  default(conn: FakeConn, msg: Msg): void {
    conn.reply(msg, CANNED[msg.cmd] ?? {});
  }

  last(cmd: string): Msg {
    for (let i = this.received.length - 1; i >= 0; i--) if (this.received[i].cmd === cmd) return this.received[i];
    throw new Error(`fake server never received ${cmd}`);
  }

  sentParams(cmd: string): Msg {
    const { id: _id, ...rest } = this.last(cmd);
    return rest;
  }

  async close(): Promise<void> {
    for (const conn of this.connections) conn.socket.destroy();
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}

export async function waitUntil(predicate: () => boolean, timeout = 3000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error("condition not met in time");
    await new Promise((r) => setTimeout(r, 10));
  }
}

export async function freePort(): Promise<number> {
  const server = createServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  await new Promise<void>((resolve) => server.close(() => resolve()));
  return port;
}

export const withoutId = (msgs: Msg[]): Msg[] => msgs.map(({ id: _id, ...rest }) => rest);
