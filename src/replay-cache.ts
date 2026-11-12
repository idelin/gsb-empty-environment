/**
 * 按 nonce 记录已消费的握手令牌。
 * 过期条目随容量/时间惰性清理，避免握手重放永久占用内存。
 */
export interface ReplayCache {
  has(nonce: string): boolean;
  add(nonce: string, expiresAt: number): void;
  size: number;
}

export class InMemoryReplayCache implements ReplayCache {
  private readonly seen = new Map<string, number>();
  private readonly maxEntries: number;
  private readonly clock: () => number;

  constructor(maxEntries = 4096, clock: () => number = () => Date.now()) {
    this.maxEntries = maxEntries;
    this.clock = clock;
  }

 get size(): number {
    return this.seen.size;
  }

  has(nonce: string): boolean {
    const expiresAt = this.seen.get(nonce);
    if (expiresAt === undefined) {
      return false;
    }
    if (expiresAt <= this.clock()) {
      this.seen.delete(nonce);
      return false;
    }
    return true;
  }

  add(nonce: string, expiresAt: number): void {
    this.prune(this.clock());
    this.seen.set(nonce, expiresAt);
  }

  private prune(now: number): void {
    for (const [nonce, expiresAt] of this.seen) {
      if (expiresAt <= now) {
        this.seen.delete(nonce);
      }
    }
    while (this.seen.size >= this.maxEntries) {
      const oldest = this.seen.keys().next().value;
      if (oldest === undefined) {
        break;
      }
      this.seen.delete(oldest);
    }
  }
}
