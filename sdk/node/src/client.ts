import { readFile, writeFile } from "node:fs/promises";
import { createConnection, type Socket } from "node:net";
import { extname } from "node:path";
import type { Params } from "./args.js";
import { DroidlineError, errorFromResponse } from "./errors.js";
import { Element } from "./element.js";
import { ClientCommands, DeviceCommands, type DeviceInfo, type Node, type Notification, type ReturnKind } from "./generated.js";

export const DEFAULT_HOST = "127.0.0.1";
export const DEFAULT_PORT = 8780;

export interface ClientOptions {
  host?: string;
  port?: number;
  token?: string;
}

/** One request in the history kept by each client, oldest first. */
export interface HistoryEntry {
  time: number;
  device: string | undefined;
  cmd: string;
  params: Params;
  ok: boolean;
  error: string | null;
  ms: number;
}

export interface LeaseOptions extends ClientOptions {
  /** This phone, by ID or name. Omit to take any free one. */
  device?: string;
  /** Seconds to wait for a phone to become free. @default 30 */
  wait?: number;
  /** The lease ends after this many seconds without a command. @default 300 */
  ttl?: number;
  /** Only phones with at least this Android API level. */
  min_sdk?: number;
  /** Only phones whose model contains this text. */
  model?: string;
}

export interface ConnectOptions extends ClientOptions {
  /** Device ID or name. Omit to use DROIDLINE_DEVICE, else the only online phone. */
  device?: string;
}

export interface NotificationFilter {
  /** Only from this app. */
  package?: string;
  /** Only if title or text contains this. */
  textContains?: string;
  /** Called with the notification, unless the callback is passed as the second argument. */
  callback?: NotificationCallback;
}

export type NotificationCallback = (notification: Notification) => unknown;

type Reply = Record<string, any>;

interface Link {
  socket: Socket;
  alive: boolean;
  pending: Map<unknown, { resolve: (reply: Reply) => void; reject: (err: Error) => void }>;
}

const IMAGE_FORMATS: Record<string, string> = { ".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg" };

const env = (name: string): string | undefined => process.env[name] || undefined;

function lost(): DroidlineError {
  return new DroidlineError(
    "The connection to the Droidline server dropped before the reply arrived. The command may or may not have run.",
    "CONNECTION_LOST",
  );
}

function fields(reply: Reply): Reply {
  const { id: _id, ok: _ok, ...rest } = reply;
  return rest;
}

function result(kind: ReturnKind, reply: Reply): unknown {
  if (kind === "value") return reply.value;
  if (kind === "fields") return fields(reply);
  return undefined;
}

function message(cmd: string, device: string | undefined, params: Params, lease?: string): Params {
  const msg: Params = device === undefined ? { cmd, ...params } : { cmd, device, ...params };
  if (lease !== undefined) msg.lease = lease;
  return msg;
}

const HISTORY_SIZE = 200;

/** Pipelines NDJSON requests over one socket and matches replies by id (PROTOCOL.md section 3). */
class Connection {
  private link?: Link;
  private opening?: Promise<Link>;
  private nextId = 1;
  private readonly subscriptions: string[] = [];
  readonly history: HistoryEntry[] = [];

  constructor(
    readonly host: string,
    readonly port: number,
    private token: string | undefined,
    private readonly onEvent: (event: Reply) => void,
    private readonly keepAlive: () => boolean,
  ) {}

  get connected(): boolean {
    return this.link?.alive === true;
  }

  async request(msg: Params): Promise<Reply> {
    const start = Date.now();
    let reply: Reply;
    try {
      reply = await this.exchange(await this.ensure(), msg);
    } catch (err) {
      this.note(msg, start, err instanceof DroidlineError ? err.code : "CONNECTION_LOST");
      throw err;
    }
    this.note(msg, start, null);
    if (msg.cmd === "subscribe") {
      const key = JSON.stringify(msg);
      if (!this.subscriptions.includes(key)) this.subscriptions.push(key);
    } else if (msg.cmd === "auth") {
      this.token = msg.token as string | undefined;
    }
    return reply;
  }

  private note(msg: Params, start: number, error: string | null): void {
    if (msg.cmd === "auth") return;
    const params: Params = {};
    for (const [k, v] of Object.entries(msg)) {
      if (["id", "cmd", "device", "lease", "token"].includes(k)) continue;
      params[k] = typeof v === "string" && v.length > 200 ? `<${v.length} characters>` : v;
    }
    this.history.push({ time: start, device: msg.device as string | undefined, cmd: String(msg.cmd), params, ok: error === null, error, ms: Date.now() - start });
    if (this.history.length > HISTORY_SIZE) this.history.shift();
  }

  async subscribeOnce(msg: Params): Promise<void> {
    if (!this.subscriptions.includes(JSON.stringify(msg))) await this.request(msg);
  }

  ensure(): Promise<Link> {
    if (this.link?.alive) return Promise.resolve(this.link);
    this.opening ??= this.open().finally(() => {
      this.opening = undefined;
    });
    return this.opening;
  }

  private async open(): Promise<Link> {
    const socket = await new Promise<Socket>((resolve, reject) => {
      const s = createConnection({ host: this.host, port: this.port });
      const fail = (reason: string) => {
        s.destroy();
        reject(
          new DroidlineError(
            `Cannot reach the Droidline server at ${this.host}:${this.port} (${reason}). Start it with \`droidline serve\`.`,
            "SERVER_NOT_RUNNING",
          ),
        );
      };
      s.setTimeout(5000, () => fail("timed out"));
      s.once("error", (err: NodeJS.ErrnoException) => fail(err.code ?? err.message));
      s.once("connect", () => {
        s.setTimeout(0);
        s.removeAllListeners("error");
        resolve(s);
      });
    });
    socket.setNoDelay(true);
    socket.setEncoding("utf8");
    const link: Link = { socket, alive: true, pending: new Map() };
    let buffer = "";
    socket.on("data", (chunk: string) => {
      buffer += chunk;
      if (!chunk.includes("\n")) return;
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      for (const line of lines) this.dispatch(link, line);
    });
    // "close" always follows "error" and fails whatever is still pending.
    socket.on("error", () => {});
    socket.on("close", () => this.drop(link));
    try {
      // Auth must be the first line, and subscriptions belong to the socket, so a reconnect
      // replays both before any other request goes out.
      if (this.token) await this.exchange(link, { cmd: "auth", token: this.token });
      for (const sub of this.subscriptions) await this.exchange(link, JSON.parse(sub) as Params);
    } catch (err) {
      this.drop(link);
      throw err;
    }
    this.link = link;
    this.updateRef(link);
    return link;
  }

  private exchange(link: Link, msg: Params): Promise<Reply> {
    if (!link.alive) return Promise.reject(lost());
    const id = this.nextId++;
    return new Promise<Reply>((resolve, reject) => {
      link.pending.set(id, { resolve, reject });
      this.updateRef(link);
      link.socket.write(JSON.stringify({ id, ...msg }) + "\n");
    });
  }

  private dispatch(link: Link, line: string): void {
    let parsed: unknown;
    try {
      parsed = JSON.parse(line);
    } catch {
      return;
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return;
    const reply = parsed as Reply;
    // Late results carry the request id too, so the event check comes first.
    if ("event" in reply) {
      this.onEvent(reply);
      return;
    }
    const waiter = link.pending.get(reply.id);
    if (!waiter) return;
    link.pending.delete(reply.id);
    this.updateRef(link);
    if (reply.ok === false) waiter.reject(errorFromResponse(reply));
    else waiter.resolve(reply);
  }

  private drop(link: Link): void {
    if (!link.alive) return;
    link.alive = false;
    if (this.link === link) this.link = undefined;
    link.socket.destroy();
    const waiters = [...link.pending.values()];
    link.pending.clear();
    for (const w of waiters) w.reject(lost());
  }

  /** An idle socket must not keep the process alive unless someone listens for events. */
  updateRef(link = this.link): void {
    if (!link?.alive) return;
    if (link.pending.size > 0 || this.keepAlive()) link.socket.ref();
    else link.socket.unref();
  }

  close(): void {
    if (this.link) this.drop(this.link);
  }
}

const connections = new WeakMap<Droidline, Connection>();
const ownedDevices = new WeakSet<Device>();

function conn(client: Droidline): Connection {
  return connections.get(client)!;
}

/**
 * Client for the local Droidline server. Emits server events by kind ("device",
 * "notification", "screen", "toast", "result") and every event as "*". Only "result" arrives
 * without subscribe(). Options fall back to DROIDLINE_HOST, DROIDLINE_PORT and DROIDLINE_TOKEN,
 * then to 127.0.0.1:8780 without a token. The socket opens on the first call and reopens after a drop.
 */
export class Droidline extends ClientCommands {
  readonly host: string;
  readonly port: number;

  constructor(options: ClientOptions = {}) {
    super();
    this.host = options.host ?? env("DROIDLINE_HOST") ?? DEFAULT_HOST;
    this.port = Number(options.port ?? env("DROIDLINE_PORT") ?? DEFAULT_PORT);
    const connection = new Connection(
      this.host,
      this.port,
      options.token ?? env("DROIDLINE_TOKEN"),
      (event) => this.deliver(event),
      () => this.eventNames().some((name) => name !== "newListener" && name !== "removeListener"),
    );
    connections.set(this, connection);
    const refresh = () => queueMicrotask(() => connection.updateRef());
    this.on("newListener", refresh);
    this.on("removeListener", refresh);
  }

  /** True while the socket to the server is open. */
  get connected(): boolean {
    return conn(this).connected;
  }

  /** A handle for one phone. Without an argument it uses DROIDLINE_DEVICE, else the only online phone. */
  device(idOrName?: string): Device {
    return new Device(this, idOrName ?? env("DROIDLINE_DEVICE"));
  }

  /** Send any command, including ones newer than this SDK. Resolves with the reply without id and ok. */
  async call(cmd: string, params: Params = {}): Promise<Reply> {
    return fields(await conn(this).request({ cmd, ...params }));
  }

  /**
   * Borrow a free phone so no other script can use it until release(). Optional: phones nobody
   * leased keep working as before. Rejects with NO_FREE_DEVICE when nothing matched within `wait`.
   */
  async lease(options: Omit<LeaseOptions, keyof ClientOptions> = {}): Promise<LeasedDevice> {
    const params: Params = {};
    for (const [k, v] of Object.entries(options)) if (v !== undefined) params[k] = v;
    const reply = fields(await conn(this).request(message("lease", undefined, params)));
    return new LeasedDevice(this, String(reply.device), String(reply.lease), reply);
  }

  /**
   * Give a leased phone back. Releasing twice is harmless. Pass `{ device }` instead of the
   * lease to free a phone whose script crashed while holding it.
   */
  async release(lease: string | { device: string }): Promise<void> {
    await conn(this).request(typeof lease === "string" ? { cmd: "release", lease } : { cmd: "release", device: lease.device });
  }

  /** The last 200 requests on this connection, oldest first. */
  get history(): HistoryEntry[] {
    return [...conn(this).history];
  }

  /** Close the socket. Calls still waiting fail with CONNECTION_LOST; a later call reconnects. */
  close(): void {
    conn(this).close();
  }

  protected async _run(cmd: string, kind: ReturnKind, params: Params): Promise<any> {
    return result(kind, await conn(this).request(message(cmd, undefined, params)));
  }

  private deliver(event: Reply): void {
    for (const name of [String(event.event), "*"]) {
      // EventEmitter throws on an "error" event nobody listens to; servers never send one.
      if (name === "error") continue;
      try {
        this.emit(name, event);
      } catch (err) {
        console.error(err);
      }
    }
  }
}

/** One phone. Every generated command method sends a request with this device's ID or name. */
export class Device extends DeviceCommands {
  readonly client: Droidline;
  readonly device: string | undefined;

  constructor(client: Droidline, device?: string) {
    super();
    this.client = client;
    this.device = device;
  }

  /** Set while this handle holds a lease; every request carries it. */
  leaseId: string | undefined;

  /** Send any command to this phone, including ones newer than this SDK. */
  async call(cmd: string, params: Params = {}): Promise<Reply> {
    return fields(await conn(this.client).request(message(cmd, this.device, params, this.leaseId)));
  }

  /** Recent requests to this phone, oldest first. */
  get history(): HistoryEntry[] {
    return this.client.history.filter((e) => this.device === undefined || e.device === this.device);
  }

  /**
   * Call `callback(notification)` for every matching notification from this phone. The phone
   * forwards only the apps allowed with notify_filter(). Resolves with a function that stops the callback.
   * @example const stop = await d.on_notification({ package: "com.kakao.talk" }, (n) => console.log(n.text))
   */
  async on_notification(filter: NotificationFilter | NotificationCallback, callback?: NotificationCallback): Promise<() => void> {
    const opts: NotificationFilter = typeof filter === "function" ? {} : filter;
    const cb = typeof filter === "function" ? filter : (callback ?? opts.callback);
    if (typeof cb !== "function") throw new TypeError("on_notification() needs a callback");
    const ids = await this.identities();
    const text = opts.textContains;
    const handler = (event: Notification) => {
      if (ids.size > 0 && !ids.has(event.device) && !ids.has(event.name)) return;
      if (opts.package !== undefined && event.package !== opts.package) return;
      if (text !== undefined && ![event.title, event.text].some((v) => String(v ?? "").includes(text))) return;
      cb(event);
    };
    this.client.on("notification", handler);
    try {
      await conn(this.client).subscribeOnce({ cmd: "subscribe", events: ["notification"] });
    } catch (err) {
      this.client.off("notification", handler);
      throw err;
    }
    return () => {
      this.client.off("notification", handler);
    };
  }

  /** @alias on_notification */
  onNotification(filter: NotificationFilter | NotificationCallback, callback?: NotificationCallback): Promise<() => void> {
    return this.on_notification(filter, callback);
  }

  /** Close the connection if connect() opened it for this device; otherwise do nothing. */
  close(): void {
    if (ownedDevices.has(this)) this.client.close();
  }

  protected async _run(cmd: string, kind: ReturnKind, params: Params): Promise<any> {
    return result(kind, await conn(this.client).request(message(cmd, this.device, params, this.leaseId)));
  }

  protected async _loadImage(image: unknown): Promise<string> {
    // A file path or the bytes of a PNG or JPEG; the wire carries base64.
    if (image instanceof Uint8Array) return Buffer.from(image).toString("base64");
    return (await readFile(String(image))).toString("base64");
  }

  protected async _runElement(cmd: string, kind: ReturnKind, params: Params): Promise<Element | Element[]> {
    const found = await this._run(cmd, kind, params);
    return Array.isArray(found) ? found.map((n: Node) => new Element(this, n)) : new Element(this, found as Node);
  }

  protected async _saveImage(cmd: string, params: Params): Promise<Buffer | string> {
    const { path, ...rest } = params;
    if (typeof path === "string" && rest.format === undefined) {
      const format = IMAGE_FORMATS[extname(path).toLowerCase()];
      if (format) rest.format = format;
    }
    const reply = (await this._run(cmd, "fields", rest)) as Reply;
    const image = Buffer.from(String(reply.data ?? ""), "base64");
    if (typeof path !== "string") return image;
    await writeFile(path, image);
    return path;
  }

  protected async _saveJson(cmd: string, params: Params): Promise<Reply> {
    const { path, ...rest } = params;
    const reply = (await this._run(cmd, "fields", rest)) as Reply;
    if (typeof path === "string") await writeFile(path, JSON.stringify(reply, null, 2), "utf8");
    return reply;
  }

  // Events name the device by ID; resolve a name so both match. Empty means any device.
  private async identities(): Promise<Set<unknown>> {
    if (this.device === undefined) return new Set();
    const ids = new Set<unknown>([this.device]);
    try {
      const list: DeviceInfo[] = await this.client.devices();
      for (const info of list) {
        if (info.id === this.device || info.name === this.device) {
          if (info.id) ids.add(info.id);
          if (info.name) ids.add(info.name);
        }
      }
    } catch (err) {
      if (!(err instanceof DroidlineError)) throw err;
    }
    return ids;
  }
}

/** A phone borrowed with lease(). Every request carries the lease; release() gives it back. */
export class LeasedDevice extends Device {
  readonly name: string;
  readonly ttl: number;

  constructor(client: Droidline, device: string, leaseId: string, info: Reply) {
    super(client, device);
    this.leaseId = leaseId;
    this.name = String(info.name ?? device);
    this.ttl = Number(info.ttl ?? 0);
  }

  /** Give the phone back. */
  async release(): Promise<void> {
    const id = this.leaseId;
    this.leaseId = undefined;
    if (id !== undefined) await this.client.release(id);
  }

  /** Release the phone, then close the connection if lease() opened it. */
  override close(): void {
    const id = this.leaseId;
    this.leaseId = undefined;
    const done = () => super.close();
    if (id === undefined) return done();
    this.client.release(id).catch(() => undefined).finally(done);
  }
}

/**
 * Like connect(), but borrows a free phone so no other script can use it.
 * @example const d = await lease({ wait: 60 }); try { ... } finally { await d.release(); d.close(); }
 */
export async function lease(options: LeaseOptions = {}): Promise<LeasedDevice> {
  const { host, port, token, ...rest } = options;
  const client = new Droidline({ host, port, token });
  try {
    const d = await client.lease(rest);
    ownedDevices.add(d);
    return d;
  } catch (err) {
    client.close();
    throw err;
  }
}

/**
 * Connect to the local Droidline server and resolve with a Device. Pass a device ID or name, or
 * options. Rejects with SERVER_NOT_RUNNING right away if the server is not running.
 */
export async function connect(options: string | ConnectOptions = {}): Promise<Device> {
  const opts: ConnectOptions = typeof options === "string" ? { device: options } : options;
  const client = new Droidline(opts);
  await conn(client).ensure();
  const d = client.device(opts.device);
  ownedDevices.add(d);
  return d;
}
