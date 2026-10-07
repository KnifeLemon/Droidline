// Per-phone frame budget from PROTOCOL.md 6.2. Lives in Durable Object memory only, so it
// starts full again after the object hibernates.
export const FRAMES_PER_SECOND = 50;
export const BURST = 200;

export class TokenBucket {
  private tokens: number;
  private last: number;

  constructor(
    nowMs: number,
    private readonly rate = FRAMES_PER_SECOND,
    private readonly burst = BURST,
  ) {
    this.tokens = burst;
    this.last = nowMs;
  }

  take(nowMs: number): boolean {
    const elapsed = Math.max(0, nowMs - this.last);
    this.last = Math.max(this.last, nowMs);
    this.tokens = Math.min(this.burst, this.tokens + (elapsed * this.rate) / 1000);
    if (this.tokens < 1) return false;
    this.tokens -= 1;
    return true;
  }
}
