import { describe, expect, it } from "vitest";
import vectors from "../../../spec/test-vectors.json";
import {
  base64url,
  bearerFrom,
  deviceTicket,
  enrollTicket,
  isValidExpiry,
  isValidId,
  timingSafeEqual,
  verifyDeviceTicket,
  verifyEnrollTicket,
  verifyRelayToken,
} from "../src/tickets";

const { relay_token: token, device_ticket: device, enroll_ticket: enroll } = vectors.relay;
const expiry = String(enroll.expiry);

describe("tickets match spec/test-vectors.json", () => {
  it("device ticket", async () => {
    expect(await deviceTicket(token, device.server, device.device)).toBe(device.ticket);
  });

  it("enroll ticket", async () => {
    expect(await enrollTicket(token, enroll.server, expiry)).toBe(enroll.ticket);
  });
});

describe("verifyDeviceTicket", () => {
  it("accepts the vector", async () => {
    expect(await verifyDeviceTicket(token, device.server, device.device, device.ticket)).toBe(true);
  });

  it("rejects another device, server or token", async () => {
    expect(await verifyDeviceTicket(token, device.server, "a1b2c3d5", device.ticket)).toBe(false);
    expect(await verifyDeviceTicket(token, "k3j9d0a2mr", device.device, device.ticket)).toBe(false);
    expect(await verifyDeviceTicket("relay-test-tokeN", device.server, device.device, device.ticket)).toBe(false);
  });

  it("rejects padded, truncated and standard-alphabet forms", async () => {
    expect(await verifyDeviceTicket(token, device.server, device.device, `${device.ticket}=`)).toBe(false);
    expect(await verifyDeviceTicket(token, device.server, device.device, device.ticket.slice(0, -1))).toBe(false);
    const standard = device.ticket.replace(/-/g, "+").replace(/_/g, "/");
    if (standard !== device.ticket) {
      expect(await verifyDeviceTicket(token, device.server, device.device, standard)).toBe(false);
    }
  });

  it("does not accept an enroll ticket as a device ticket", async () => {
    expect(await verifyDeviceTicket(token, enroll.server, expiry, enroll.ticket)).toBe(false);
  });
});

describe("verifyEnrollTicket", () => {
  it("accepts the vector up to and including the expiry second", async () => {
    expect(await verifyEnrollTicket(token, enroll.server, expiry, enroll.ticket, enroll.expiry - 600)).toBe("ok");
    expect(await verifyEnrollTicket(token, enroll.server, expiry, enroll.ticket, enroll.expiry)).toBe("ok");
  });

  it("reports expiry only for a genuine ticket", async () => {
    expect(await verifyEnrollTicket(token, enroll.server, expiry, enroll.ticket, enroll.expiry + 1)).toBe("expired");
    expect(await verifyEnrollTicket(token, enroll.server, expiry, device.ticket, enroll.expiry + 1)).toBe("invalid");
  });

  it("rejects a moved expiry", async () => {
    const later = String(enroll.expiry + 3600);
    expect(await verifyEnrollTicket(token, enroll.server, later, enroll.ticket, enroll.expiry)).toBe("invalid");
  });

  it("does not accept a device ticket as an enroll ticket", async () => {
    expect(await verifyEnrollTicket(token, device.server, device.device, device.ticket, 0)).toBe("invalid");
  });
});

describe("verifyRelayToken", () => {
  it("accepts only the exact token", async () => {
    expect(await verifyRelayToken(token, token)).toBe(true);
    expect(await verifyRelayToken(token, `${token} `)).toBe(false);
    expect(await verifyRelayToken(token, token.slice(1))).toBe(false);
    expect(await verifyRelayToken(token, "")).toBe(false);
  });
});

describe("helpers", () => {
  it("timingSafeEqual", () => {
    expect(timingSafeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 3]))).toBe(true);
    expect(timingSafeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 4]))).toBe(false);
    expect(timingSafeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2]))).toBe(false);
    expect(timingSafeEqual(new Uint8Array(), new Uint8Array())).toBe(true);
  });

  it("base64url has no padding and uses the URL alphabet", () => {
    expect(base64url(new Uint8Array([0xfb, 0xff]))).toBe("-_8");
    expect(base64url(new Uint8Array([0x66]))).toBe("Zg");
  });

  it("bearerFrom", () => {
    expect(bearerFrom("Bearer abc")).toBe("abc");
    expect(bearerFrom("bearer abc")).toBe("abc");
    expect(bearerFrom("Basic abc")).toBeNull();
    expect(bearerFrom("Bearer")).toBeNull();
    expect(bearerFrom("Bearer a b")).toBeNull();
    expect(bearerFrom(null)).toBeNull();
  });

  it("isValidId", () => {
    expect(isValidId("k3j9d0a2mq")).toBe(true);
    expect(isValidId("a".repeat(32))).toBe(true);
    expect(isValidId("a".repeat(33))).toBe(false);
    expect(isValidId("")).toBe(false);
    expect(isValidId("A1B2")).toBe(false);
    expect(isValidId("a1-b2")).toBe(false);
    expect(isValidId("a1b2\n")).toBe(false);
    expect(isValidId(null)).toBe(false);
  });

  it("isValidExpiry", () => {
    expect(isValidExpiry("1791360000")).toBe(true);
    expect(isValidExpiry("01791360000")).toBe(false);
    expect(isValidExpiry("0")).toBe(false);
    expect(isValidExpiry("-1")).toBe(false);
    expect(isValidExpiry("1e9")).toBe(false);
    expect(isValidExpiry("1".repeat(13))).toBe(false);
  });
});
