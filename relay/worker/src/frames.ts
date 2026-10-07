// Routing decisions, kept free of Durable Object APIs so they can be unit tested.
// A phone's line stays an opaque string; the server decodes it back unchanged (PROTOCOL.md 6.1).
import type { TokenBucket } from "./ratelimit";
import { isValidExpiry, isValidId } from "./tickets";

// PROTOCOL.md section 1 caps a line at 1 MiB. A server frame wraps one line in JSON, and escaping
// can grow it, so the outer frame gets twice the room and the line inside is checked on its own.
export const MAX_LINE_BYTES = 1024 * 1024;
export const MAX_SERVER_FRAME_BYTES = 2 * MAX_LINE_BYTES;

export const CLOSE_NORMAL = 1000;
export const CLOSE_UNSUPPORTED_DATA = 1003;
export const CLOSE_TOO_BIG = 1009;
export const CLOSE_REPLACED = 4000;
export const CLOSE_RATE_LIMITED = 4008;

export const SERVER_TAG = "server";
export const DEVICE_TAG = "device";

export function deviceTag(device: string): string {
  return `device:${device}`;
}

export type Role = "server" | "device";

export type ConnectRequest =
  | { role: "server"; server: string }
  | { role: "device"; server: string; device: string; expiry: string | null };

export interface Attachment {
  role: Role;
  device?: string;
  enroll?: boolean;
  // Set once the relay has reported this socket's end, so a later close or error event is a no-op.
  ended?: boolean;
}

export function routeOf(pathname: string): Role | null {
  if (pathname === "/v1/server") return "server";
  if (pathname === "/v1/device") return "device";
  return null;
}

export function parseConnect(role: Role, params: URLSearchParams): ConnectRequest | null {
  const server = params.get("server");
  if (!isValidId(server)) return null;
  if (role === "server") return { role, server };

  const device = params.get("device");
  if (!isValidId(device)) return null;
  const expiry = params.get("expiry");
  if (expiry !== null && !isValidExpiry(expiry)) return null;
  return { role, server, device, expiry };
}

export function utf8Length(text: string): number {
  let bytes = 0;
  for (let i = 0; i < text.length; i++) {
    const unit = text.charCodeAt(i);
    if (unit < 0x80) bytes += 1;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xd800 && unit <= 0xdbff && i + 1 < text.length) {
      const next = text.charCodeAt(i + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        bytes += 4;
        i++;
      } else {
        bytes += 3;
      }
    } else bytes += 3;
  }
  return bytes;
}

export function fitsUtf8(text: string, maxBytes: number): boolean {
  // Every UTF-16 unit costs 1 to 3 bytes, so most frames are settled without walking them.
  if (text.length * 3 <= maxBytes) return true;
  if (text.length > maxBytes) return false;
  return utf8Length(text) <= maxBytes;
}

export function lineFrame(device: string, line: string): string {
  return JSON.stringify({ device, line });
}

export function eventFrame(device: string, event: "open" | "close"): string {
  return JSON.stringify({ device, event });
}

export type PhoneRoute =
  | { action: "forward"; frame: string }
  | { action: "close"; code: number; reason: string };

export function routePhoneMessage(
  device: string,
  message: string | ArrayBuffer,
  bucket: TokenBucket,
  nowMs: number,
): PhoneRoute {
  if (typeof message !== "string") {
    return { action: "close", code: CLOSE_UNSUPPORTED_DATA, reason: "binary frames are not supported" };
  }
  if (!fitsUtf8(message, MAX_LINE_BYTES)) {
    return { action: "close", code: CLOSE_TOO_BIG, reason: "frame exceeds 1 MiB" };
  }
  // Dropping one frame would break the envelope sequence anyway, so an empty bucket closes the link.
  if (!bucket.take(nowMs)) {
    return { action: "close", code: CLOSE_RATE_LIMITED, reason: "rate limit exceeded" };
  }
  return { action: "forward", frame: lineFrame(device, message) };
}

export type ServerRoute =
  | { action: "deliver"; device: string; line: string | null; close: boolean }
  | { action: "ignore" }
  | { action: "close"; code: number; reason: string };

export function routeServerMessage(message: string | ArrayBuffer): ServerRoute {
  if (typeof message !== "string") {
    return { action: "close", code: CLOSE_UNSUPPORTED_DATA, reason: "binary frames are not supported" };
  }
  if (!fitsUtf8(message, MAX_SERVER_FRAME_BYTES)) {
    return { action: "close", code: CLOSE_TOO_BIG, reason: "frame too large" };
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(message);
  } catch {
    return { action: "ignore" };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return { action: "ignore" };

  const frame = parsed as Record<string, unknown>;
  const device = frame.device;
  if (typeof device !== "string" || !isValidId(device)) return { action: "ignore" };

  const line = typeof frame.line === "string" ? frame.line : null;
  const close = frame.close === true;
  if (line === null && !close) return { action: "ignore" };
  if (line !== null && !fitsUtf8(line, MAX_LINE_BYTES)) {
    return { action: "close", code: CLOSE_TOO_BIG, reason: "line exceeds 1 MiB" };
  }
  return { action: "deliver", device, line, close };
}
