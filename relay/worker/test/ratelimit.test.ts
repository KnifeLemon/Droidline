import { describe, expect, it } from "vitest";
import { BURST, FRAMES_PER_SECOND, TokenBucket } from "../src/ratelimit";

function drain(bucket: TokenBucket, nowMs: number): number {
  let taken = 0;
  while (bucket.take(nowMs)) taken++;
  return taken;
}

describe("TokenBucket", () => {
  it("allows a burst of 200 at once", () => {
    expect(drain(new TokenBucket(0), 0)).toBe(BURST);
  });

  it("refills at 50 frames per second", () => {
    const bucket = new TokenBucket(0);
    drain(bucket, 0);
    expect(drain(bucket, 1000)).toBe(FRAMES_PER_SECOND);
    expect(bucket.take(1010)).toBe(false);
    expect(bucket.take(1020)).toBe(true);
  });

  it("never holds more than the burst", () => {
    const bucket = new TokenBucket(0);
    expect(drain(bucket, 3_600_000)).toBe(BURST);
  });

  it("sustains exactly 50 frames per second after the burst", () => {
    const bucket = new TokenBucket(0);
    drain(bucket, 0);
    for (let ms = 20; ms <= 10_000; ms += 20) {
      expect(bucket.take(ms)).toBe(true);
    }
    expect(bucket.take(10_000)).toBe(false);
  });

  it("ignores a clock that steps backwards", () => {
    const bucket = new TokenBucket(5000);
    drain(bucket, 5000);
    expect(bucket.take(1000)).toBe(false);
    expect(drain(bucket, 5000)).toBe(0);
    expect(drain(bucket, 6000)).toBe(FRAMES_PER_SECOND);
  });
});
