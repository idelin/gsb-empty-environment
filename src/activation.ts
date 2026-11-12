import type { ActivationToken } from './types.ts';
import type { Clock } from './clock.ts';

let sequence = 0;

function nextId(prefix: string): string {
  sequence += 1;
  return `${prefix}-${sequence}`;
}

/**
 * 管理用户手势产生的瞬态激活。
 * 激活与手势、标签页绑定，按 TTL 过期；令牌本身不被单次使用销毁，
 * 同一手势窗口内的多个动作可共享它，过期后一律判定失效。
 */
export class ActivationTracker {
  private readonly tokens = new Map<string, ActivationToken>();
  private readonly clock: Clock;
  private readonly ttlMs: number;

  constructor(clock: Clock, ttlMs: number) {
    this.clock = clock;
    this.ttlMs = ttlMs;
    if (ttlMs <= 0) {
      throw new RangeError('activation ttl must be positive');
    }
  }

  signalUserGesture(tabId: string, gestureId?: string): ActivationToken {
    const now = this.clock.now();
    const token: ActivationToken = {
      tokenId: nextId('act'),
      gestureId: gestureId ?? nextId('gesture'),
      tabId,
      issuedAt: now,
      expiresAt: now + this.ttlMs,
    };
    this.tokens.set(token.tokenId, token);
    return token;
  }

  get(tokenId: string): ActivationToken | undefined {
    return this.tokens.get(tokenId);
  }

  /** 某标签页最近一次手势产生的激活（可能已过期）。 */
  latestForTab(tabId: string): ActivationToken | null {
    let latest: ActivationToken | null = null;
    for (const token of this.tokens.values()) {
      if (token.tabId !== tabId) {
        continue;
      }
      if (latest === null || token.issuedAt > latest.issuedAt) {
        latest = token;
      }
    }
    return latest;
  }

  isLive(tokenId: string, now: number = this.clock.now()): boolean {
    const token = this.tokens.get(tokenId);
    return token !== undefined && now < token.expiresAt;
  }

  /**
   * 严格复核：令牌必须仍然存在、未过期，且还是该标签页上最近一次手势，
   * 中间出现过更新的手势（或导航后重新激活）即视为被取代。
   */
  isCurrentActivation(tokenId: string): ActivationToken | null {
    const token = this.tokens.get(tokenId);
    if (!token || this.clock.now() >= token.expiresAt) {
      return null;
    }
    const latest = this.latestForTab(token.tabId);
    if (!latest || latest.tokenId !== token.tokenId) {
      return null;
    }
    return token;
  }

  size(): number {
    return this.tokens.size;
  }
}
