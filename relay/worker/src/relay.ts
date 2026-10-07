import { DurableObject } from "cloudflare:workers";
import type { Env } from "./env";
import {
  type Attachment,
  type Role,
  CLOSE_NORMAL,
  CLOSE_REPLACED,
  DEVICE_TAG,
  SERVER_TAG,
  deviceTag,
  eventFrame,
  parseConnect,
  routeOf,
  routePhoneMessage,
  routeServerMessage,
} from "./frames";
import { TokenBucket } from "./ratelimit";

// One object per server id, holding the PC's socket and its phones' sockets via the hibernation API.
// Fields on `this` are lost when the object hibernates; per-socket state lives in the attachment.
export class Relay extends DurableObject<Env> {
  private readonly buckets = new Map<string, TokenBucket>();

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const role = routeOf(url.pathname);
    const connect = role === null ? null : parseConnect(role, url.searchParams);
    if (connect === null) return new Response("bad request", { status: 400 });

    if (connect.role === "server") {
      const socket = this.open();
      this.acceptServer(socket.server);
      return new Response(null, { status: 101, webSocket: socket.client });
    }

    const enroll = connect.expiry !== null;
    // An enroll ticket is not bound to a device id, so whoever holds a pairing QR could otherwise
    // kick a paired phone off the relay until the ticket expires.
    const current = this.live(deviceTag(connect.device))[0];
    if (enroll && current !== undefined && !attachmentOf(current)?.enroll) {
      return new Response("device is connected with a device ticket", { status: 409 });
    }

    const socket = this.open();
    this.acceptDevice(socket.server, connect.device, enroll);
    return new Response(null, { status: 101, webSocket: socket.client });
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    const attachment = attachmentOf(ws);
    if (attachment === null || attachment.ended) return;

    if (attachment.role === "device" && attachment.device !== undefined) {
      this.fromPhone(ws, attachment.device, message);
    } else if (attachment.role === "server") {
      this.fromServer(ws, message);
    }
  }

  async webSocketClose(ws: WebSocket): Promise<void> {
    this.finish(ws);
  }

  async webSocketError(ws: WebSocket): Promise<void> {
    this.finish(ws);
  }

  private open(): { client: WebSocket; server: WebSocket } {
    const pair = new WebSocketPair();
    return { client: pair[0], server: pair[1] };
  }

  private acceptServer(socket: WebSocket): void {
    for (const old of this.live(SERVER_TAG)) {
      this.drop(old, CLOSE_REPLACED, "replaced by a newer server connection");
    }
    this.ctx.acceptWebSocket(socket, [SERVER_TAG]);
    socket.serializeAttachment({ role: "server" } satisfies Attachment);

    for (const phone of this.live(DEVICE_TAG)) {
      const device = attachmentOf(phone)?.device;
      if (device !== undefined) send(socket, eventFrame(device, "open"));
    }
    this.logCounts("connect", "server");
  }

  private acceptDevice(socket: WebSocket, device: string, enroll: boolean): void {
    // The old socket's "close" event reaches the server before the new socket's "open".
    for (const old of this.live(deviceTag(device))) {
      this.drop(old, CLOSE_REPLACED, "replaced by a newer connection for this device");
    }
    this.ctx.acceptWebSocket(socket, [DEVICE_TAG, deviceTag(device)]);
    socket.serializeAttachment({ role: "device", device, enroll } satisfies Attachment);

    this.toServer(eventFrame(device, "open"));
    this.logCounts("connect", "device");
  }

  private fromPhone(ws: WebSocket, device: string, message: string | ArrayBuffer): void {
    const now = Date.now();
    let bucket = this.buckets.get(device);
    if (bucket === undefined) {
      bucket = new TokenBucket(now);
      this.buckets.set(device, bucket);
    }

    const route = routePhoneMessage(device, message, bucket, now);
    if (route.action === "close") {
      this.drop(ws, route.code, route.reason);
      return;
    }
    // With no server connected the frame is dropped; the phone's resume buffer replays it later.
    this.toServer(route.frame);
  }

  private fromServer(ws: WebSocket, message: string | ArrayBuffer): void {
    const route = routeServerMessage(message);
    if (route.action === "close") {
      this.drop(ws, route.code, route.reason);
      return;
    }
    if (route.action === "ignore") return;

    const phone = this.live(deviceTag(route.device))[0];
    if (phone === undefined) return;
    if (route.line !== null) send(phone, route.line);
    if (route.close) this.drop(phone, CLOSE_NORMAL, "closed by server");
  }

  private toServer(frame: string): void {
    const server = this.live(SERVER_TAG)[0];
    if (server !== undefined) send(server, frame);
  }

  private live(tag: string): WebSocket[] {
    return this.ctx.getWebSockets(tag).filter((ws) => {
      const attachment = attachmentOf(ws);
      return attachment !== null && !attachment.ended && ws.readyState === WebSocket.OPEN;
    });
  }

  private drop(ws: WebSocket, code: number, reason: string): void {
    this.finish(ws);
    try {
      ws.close(code, reason);
    } catch {
      // Already closed by the peer.
    }
  }

  // Reports a socket's end exactly once, whichever of drop, close or error gets here first.
  private finish(ws: WebSocket): void {
    const attachment = attachmentOf(ws);
    if (attachment === null || attachment.ended) return;
    try {
      ws.serializeAttachment({ ...attachment, ended: true } satisfies Attachment);
    } catch {
      // The socket is already gone, so nothing will read the flag.
    }
    if (attachment.role === "device" && attachment.device !== undefined) {
      this.toServer(eventFrame(attachment.device, "close"));
    }
    this.logCounts("disconnect", attachment.role);
  }

  // Counts only. Frame contents, ids and addresses never reach the log.
  private logCounts(event: "connect" | "disconnect", role: Role): void {
    const server = this.live(SERVER_TAG).length;
    const devices = this.live(DEVICE_TAG).length;
    console.log(JSON.stringify({ event, role, server, devices }));
  }
}

function attachmentOf(ws: WebSocket): Attachment | null {
  const value: unknown = ws.deserializeAttachment();
  if (typeof value !== "object" || value === null) return null;
  return value as Attachment;
}

function send(ws: WebSocket, frame: string): void {
  try {
    ws.send(frame);
  } catch {
    // The peer went away between the lookup and the send; its close event follows.
  }
}
