import type { Env } from "./env";
import { type ConnectRequest, parseConnect, routeOf } from "./frames";
import { bearerFrom, verifyDeviceTicket, verifyEnrollTicket, verifyRelayToken } from "./tickets";

export { Relay } from "./relay";

function text(status: number, body: string, headers: Record<string, string> = {}): Response {
  return new Response(body, { status, headers: { "content-type": "text/plain; charset=utf-8", ...headers } });
}

// Header only: the PC and the Android agent can both set headers on an upgrade, and a token in the
// query string would land in access logs.
async function authorize(connect: ConnectRequest, bearer: string, relayToken: string): Promise<Response | null> {
  if (connect.role === "server") {
    return (await verifyRelayToken(relayToken, bearer)) ? null : unauthorized("invalid relay token");
  }
  if (connect.expiry === null) {
    const valid = await verifyDeviceTicket(relayToken, connect.server, connect.device, bearer);
    return valid ? null : unauthorized("invalid ticket");
  }
  const nowSeconds = Math.floor(Date.now() / 1000);
  const check = await verifyEnrollTicket(relayToken, connect.server, connect.expiry, bearer, nowSeconds);
  if (check === "ok") return null;
  return unauthorized(check === "expired" ? "ticket expired" : "invalid ticket");
}

function unauthorized(reason: string): Response {
  return text(401, reason, { "www-authenticate": 'Bearer realm="droidline-relay"' });
}

export default {
  async fetch(request, env): Promise<Response> {
    const url = new URL(request.url);
    if (request.method !== "GET") return text(404, "not found");
    if (url.pathname === "/healthz") return text(200, "ok");

    const role = routeOf(url.pathname);
    if (role === null) return text(404, "not found");
    if (request.headers.get("upgrade")?.toLowerCase() !== "websocket") {
      return text(426, "websocket upgrade required");
    }

    const connect = parseConnect(role, url.searchParams);
    if (connect === null) return text(400, "invalid server, device or expiry");
    if (!env.RELAY_TOKEN) return text(503, "RELAY_TOKEN is not set on this relay");

    const bearer = bearerFrom(request.headers.get("authorization"));
    if (bearer === null) return unauthorized("missing bearer");
    const denied = await authorize(connect, bearer, env.RELAY_TOKEN);
    if (denied !== null) return denied;

    return env.RELAY.get(env.RELAY.idFromName(connect.server)).fetch(request);
  },
} satisfies ExportedHandler<Env>;
