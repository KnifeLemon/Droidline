import { writeFile } from "node:fs/promises";
import { createConnection, type Socket } from "node:net";
import { extname } from "node:path";
import type { Params } from "./args.js";
import { DroidlineError, errorFromResponse } from "./errors.js";
import { ClientCommands, DeviceCommands, type DeviceInfo, type Notification, type ReturnKind } from "./generated.js";

export const DEFAULT_HOST = "127.0.0.1";
export const DEFAULT_PORT = 8780;

export interface ClientOptions {
  host?: string;
  port?: number;
  token?: string;
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

function message(cmd: string, device: string | undefined, params: Params): Params {
  return device === undefined ? { cmd, ...params } : { cmd, device, ...params };
}

/** Pipelines NDJSON requests over one socket and matches replies by id (PROTOCOL.md section 3). */
class Connection {
  private link?: Link;
  private opening?: Promise<Link>;
  private nextId = 1;
  private readonly subscriptions: string[] = [];

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
    const reply = await this.exchange(await this.ensure(), msg);
    if (msg.cmd === "subscribe") {
      const key = JSON.stringify(msg);
      if (!this.subscriptions.includes(key)) this.subscriptions.push(key);
    } else if (msg.cmd === "auth") {
      this.token = msg.token as string | undefined;
    }
    return reply;
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

  /** Send any command to this phone, including ones newer than this SDK. */
  async call(cmd: string, params: Params = {}): Promise<Reply> {
    return fields(await conn(this.client).request(message(cmd, this.device, params)));
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
    return result(kind, await conn(this.client).request(message(cmd, this.device, params)));
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
