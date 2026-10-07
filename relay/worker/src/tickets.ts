// Bearer checks for the two relay routes (PROTOCOL.md section 6).
// Everything here is a pure function over Web Crypto so it runs the same in Workers and in Node tests.

const encoder = new TextEncoder();

const ID_PATTERN = /^[a-z0-9]{1,32}$/;
// Canonical unix seconds only: the ticket MAC covers the exact digits, so "0123" never matches "123".
const EXPIRY_PATTERN = /^[1-9][0-9]{0,11}$/;

export function isValidId(value: string | null | undefined): value is string {
  return typeof value === "string" && ID_PATTERN.test(value);
}

export function isValidExpiry(value: string | null | undefined): value is string {
  return typeof value === "string" && EXPIRY_PATTERN.test(value);
}

export function bearerFrom(header: string | null): string | null {
  if (header === null) return null;
  const match = /^Bearer[ ]+(\S+)[ ]*$/i.exec(header);
  return match ? match[1] : null;
}

export function base64url(bytes: Uint8Array): string {
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Runs in time that depends only on the lengths, which are public (32-byte MACs, 43-char tickets).
export function timingSafeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

async function hmacSha256(key: string, message: string): Promise<Uint8Array> {
  const cryptoKey = await crypto.subtle.importKey(
    "raw",
    encoder.encode(key),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  return new Uint8Array(await crypto.subtle.sign("HMAC", cryptoKey, encoder.encode(message)));
}

export function deviceTicket(relayToken: string, server: string, device: string): Promise<string> {
  return hmacSha256(relayToken, `droidline device|${server}|${device}`).then(base64url);
}

export function enrollTicket(relayToken: string, server: string, expiry: string): Promise<string> {
  return hmacSha256(relayToken, `droidline enroll|${server}|${expiry}`).then(base64url);
}

function sameText(expected: string, presented: string): boolean {
  return timingSafeEqual(encoder.encode(expected), encoder.encode(presented));
}

export async function verifyDeviceTicket(
  relayToken: string,
  server: string,
  device: string,
  presented: string,
): Promise<boolean> {
  return sameText(await deviceTicket(relayToken, server, device), presented);
}

// "expired" is reported only for a genuine ticket, so it tells a forger nothing.
export async function verifyEnrollTicket(
  relayToken: string,
  server: string,
  expiry: string,
  presented: string,
  nowSeconds: number,
): Promise<"ok" | "invalid" | "expired"> {
  if (!sameText(await enrollTicket(relayToken, server, expiry), presented)) return "invalid";
  return nowSeconds <= Number(expiry) ? "ok" : "expired";
}

// Hashing both sides first keeps the comparison length-independent, so a wrong guess
// does not reveal how long the configured token is.
export async function verifyRelayToken(relayToken: string, presented: string): Promise<boolean> {
  const [expected, actual] = await Promise.all([
    crypto.subtle.digest("SHA-256", encoder.encode(relayToken)),
    crypto.subtle.digest("SHA-256", encoder.encode(presented)),
  ]);
  return timingSafeEqual(new Uint8Array(expected), new Uint8Array(actual));
}
