import { describe, expect, it } from "vitest";
import {
  CLOSE_RATE_LIMITED,
  CLOSE_TOO_BIG,
  CLOSE_UNSUPPORTED_DATA,
  MAX_LINE_BYTES,
  MAX_SERVER_FRAME_BYTES,
  deviceTag,
  eventFrame,
  fitsUtf8,
  lineFrame,
  parseConnect,
  routeOf,
  routePhoneMessage,
  routeServerMessage,
  utf8Length,
} from "../src/frames";
import { BURST, TokenBucket } from "../src/ratelimit";

const envelope = '{"seq":5,"blob":"Guv5VrDppIhhuou_GKd7V-4I0q7N1YzU"}';

describe("connect parsing", () => {
  it("maps paths to roles", () => {
    expect(routeOf("/v1/server")).toBe("server");
    expect(routeOf("/v1/device")).toBe("device");
    expect(routeOf("/v1/device/")).toBeNull();
    expect(routeOf("/")).toBeNull();
  });

  it("parses a server connect", () => {
    expect(parseConnect("server", new URLSearchParams("server=k3j9d0a2mq"))).toEqual({
      role: "server",
      server: "k3j9d0a2mq",
    });
    expect(parseConnect("server", new URLSearchParams("server=K3J9"))).toBeNull();
    expect(parseConnect("server", new URLSearchParams(""))).toBeNull();
  });

  it("parses device and enroll connects", () => {
    expect(parseConnect("device", new URLSearchParams("server=k3j9d0a2mq&device=a1b2c3d4"))).toEqual({
      role: "device",
      server: "k3j9d0a2mq",
      device: "a1b2c3d4",
      expiry: null,
    });
    expect(
      parseConnect("device", new URLSearchParams("server=k3j9d0a2mq&device=a1b2c3d4&expiry=1791360000")),
    ).toMatchObject({ expiry: "1791360000" });
  });

  it("rejects bad device ids and expiries", () => {
    expect(parseConnect("device", new URLSearchParams("server=k3j9d0a2mq"))).toBeNull();
    expect(parseConnect("device", new URLSearchParams("server=k3j9d0a2mq&device=a1b2%2Fc3"))).toBeNull();
    expect(parseConnect("device", new URLSearchParams("server=k3j9d0a2mq&device=a1b2c3d4&expiry=soon"))).toBeNull();
    expect(parseConnect("device", new URLSearchParams("server=k3j9d0a2mq&device=a1b2c3d4&expiry="))).toBeNull();
  });
});

describe("phone to server", () => {
  it("wraps the line as a string so the server gets the exact text back", () => {
    const route = routePhoneMessage("a1b2c3d4", envelope, new TokenBucket(0), 0);
    expect(route).toEqual({ action: "forward", frame: lineFrame("a1b2c3d4", envelope) });
    if (route.action !== "forward") throw new Error("expected forward");
    expect(JSON.parse(route.frame)).toEqual({ device: "a1b2c3d4", line: envelope });
  });

  it("keeps non-ASCII and escape-heavy lines intact", () => {
    const line = '{"hs":1,"note":"광고 닫기 \\" \\\\ \\u0000 😀"}';
    const route = routePhoneMessage("a1b2c3d4", line, new TokenBucket(0), 0);
    if (route.action !== "forward") throw new Error("expected forward");
    expect(JSON.parse(route.frame).line).toBe(line);
  });

  it("closes on binary frames with 1003", () => {
    expect(routePhoneMessage("a1b2c3d4", new ArrayBuffer(4), new TokenBucket(0), 0)).toMatchObject({
      action: "close",
      code: CLOSE_UNSUPPORTED_DATA,
    });
  });

  it("closes on frames over 1 MiB with 1009", () => {
    const atLimit = "a".repeat(MAX_LINE_BYTES);
    expect(routePhoneMessage("a1b2c3d4", atLimit, new TokenBucket(0), 0).action).toBe("forward");
    expect(routePhoneMessage("a1b2c3d4", `${atLimit}a`, new TokenBucket(0), 0)).toMatchObject({
      action: "close",
      code: CLOSE_TOO_BIG,
    });
  });

  it("counts bytes, not UTF-16 units, against the limit", () => {
    const korean = "가".repeat(Math.floor(MAX_LINE_BYTES / 3) + 1);
    expect(korean.length).toBeLessThan(MAX_LINE_BYTES);
    expect(routePhoneMessage("a1b2c3d4", korean, new TokenBucket(0), 0)).toMatchObject({ code: CLOSE_TOO_BIG });
  });

  it("closes with 4008 once the burst is spent", () => {
    const bucket = new TokenBucket(0);
    for (let i = 0; i < BURST; i++) {
      expect(routePhoneMessage("a1b2c3d4", envelope, bucket, 0).action).toBe("forward");
    }
    expect(routePhoneMessage("a1b2c3d4", envelope, bucket, 0)).toMatchObject({
      action: "close",
      code: CLOSE_RATE_LIMITED,
    });
  });
});

describe("server to phone", () => {
  it("delivers a line", () => {
    expect(routeServerMessage(lineFrame("a1b2c3d4", envelope))).toEqual({
      action: "deliver",
      device: "a1b2c3d4",
      line: envelope,
      close: false,
    });
  });

  it("delivers a close request, with or without a final line", () => {
    expect(routeServerMessage('{"device":"a1b2c3d4","close":true}')).toEqual({
      action: "deliver",
      device: "a1b2c3d4",
      line: null,
      close: true,
    });
    expect(routeServerMessage('{"device":"a1b2c3d4","line":"{}","close":true}')).toMatchObject({
      line: "{}",
      close: true,
    });
  });

  it("ignores frames it cannot route", () => {
    for (const frame of [
      "not json",
      "[]",
      "null",
      '"a1b2c3d4"',
      '{"line":"{}"}',
      '{"device":"A1B2","line":"{}"}',
      '{"device":"a1b2c3d4"}',
      '{"device":"a1b2c3d4","line":{"seq":1}}',
      '{"device":"a1b2c3d4","close":"yes"}',
      '{"device":"a1b2c3d4","event":"open"}',
    ]) {
      expect(routeServerMessage(frame)).toEqual({ action: "ignore" });
    }
  });

  it("closes the server socket on binary frames with 1003", () => {
    expect(routeServerMessage(new ArrayBuffer(1))).toMatchObject({ action: "close", code: CLOSE_UNSUPPORTED_DATA });
  });

  it("allows escaping overhead but not a line over 1 MiB", () => {
    const quotes = '"'.repeat(MAX_LINE_BYTES / 2);
    const escaped = lineFrame("a1b2c3d4", quotes);
    expect(escaped.length).toBeGreaterThan(MAX_LINE_BYTES);
    expect(routeServerMessage(escaped).action).toBe("deliver");

    expect(routeServerMessage(lineFrame("a1b2c3d4", "a".repeat(MAX_LINE_BYTES + 1)))).toMatchObject({
      action: "close",
      code: CLOSE_TOO_BIG,
    });
    expect(routeServerMessage(" ".repeat(MAX_SERVER_FRAME_BYTES + 1))).toMatchObject({
      action: "close",
      code: CLOSE_TOO_BIG,
    });
  });
});

describe("frame text", () => {
  it("events", () => {
    expect(eventFrame("a1b2c3d4", "open")).toBe('{"device":"a1b2c3d4","event":"open"}');
    expect(eventFrame("a1b2c3d4", "close")).toBe('{"device":"a1b2c3d4","event":"close"}');
  });

  it("device tags", () => {
    expect(deviceTag("a1b2c3d4")).toBe("device:a1b2c3d4");
  });

  it("utf8Length matches TextEncoder", () => {
    const encoder = new TextEncoder();
    for (const text of ["", "abc", "é", "가나다", "😀", "a😀b", "\ud800", "\udc00x", "\ud800\ud800"]) {
      expect(utf8Length(text)).toBe(encoder.encode(text).length);
    }
  });

  it("fitsUtf8 at the boundaries", () => {
    expect(fitsUtf8("abc", 3)).toBe(true);
    expect(fitsUtf8("abcd", 3)).toBe(false);
    expect(fitsUtf8("가", 3)).toBe(true);
    expect(fitsUtf8("가a", 3)).toBe(false);
  });
});
