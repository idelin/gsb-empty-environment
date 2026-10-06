import { EvalResult } from './types';

export interface CacheKeyParts {
  snapshotVersion: number;
  flagId: string;
  userId: string;
  attrVersion: number;
  closureDigest: string;
}

/**
 * Cache key: config snapshot version + flag + user + user attribute version
 * + dependency closure digest. Any config change (new snapshot), attribute
 * change (attrVersion bump), or dependency-closure change (digest) misses.
 */
export function cacheKey(parts: CacheKeyParts): string {
  return [
    `v${parts.snapshotVersion}`,
    parts.flagId,
    parts.userId,
    `attr:${parts.attrVersion}`,
    `deps:${parts.closureDigest}`,
  ].join('|');
}

/** Tiny insertion-ordered LRU for evaluation results. */
export class EvalCache {
  private readonly entries = new Map<string, EvalResult>();

  constructor(readonly maxSize = 10_000) {}

  get(key: string): EvalResult | undefined {
    const hit = this.entries.get(key);
    if (hit !== undefined) {
      this.entries.delete(key);
      this.entries.set(key, hit);
    }
    return hit;
  }

  set(key: string, value: EvalResult): void {
    this.entries.delete(key);
    this.entries.set(key, value);
    if (this.entries.size > this.maxSize) {
      const oldest = this.entries.keys().next();
      if (!oldest.done) this.entries.delete(oldest.value);
    }
  }

  clear(): void {
    this.entries.clear();
  }

  get size(): number {
    return this.entries.size;
  }
}
