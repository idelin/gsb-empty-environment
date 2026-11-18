interface CacheEntry<V> {
  value: V;
  expiresAt: number;
}

export class TtlCache<V> {
  #store = new Map<string, CacheEntry<V>>();
  #now: () => number;

  constructor(now?: () => number) {
    this.#now = now ?? (() => Date.now());
  }

  get(key: string): V | undefined {
    const entry = this.#store.get(key);
    if (!entry) return undefined;
    if (entry.expiresAt <= this.#now()) {
      this.#store.delete(key);
      return undefined;
    }
    return entry.value;
  }

  set(key: string, value: V, ttlMs: number): void {
    this.#store.set(key, { value, expiresAt: this.#now() + ttlMs });
  }

  has(key: string): boolean {
    return this.get(key) !== undefined;
  }

  delete(key: string): void {
    this.#store.delete(key);
  }
}

export interface NegativeRecord {
  name: string;
  bodyHash: string;
  reason: string;
  expiresAt: number;
}

export class NegativeCache {
  #store = new Map<string, NegativeRecord>();
  #now: () => number;

  constructor(now?: () => number) {
    this.#now = now ?? (() => Date.now());
  }

  static key(name: string, bodyHash: string): string {
    return `${name}#${bodyHash}`;
  }

  get(name: string, bodyHash: string): NegativeRecord | undefined {
    const key = NegativeCache.key(name, bodyHash);
    const entry = this.#store.get(key);
    if (!entry) return undefined;
    if (entry.expiresAt <= this.#now()) {
      this.#store.delete(key);
      return undefined;
    }
    return entry;
  }

  set(name: string, bodyHash: string, reason: string, ttlMs: number): void {
    this.#store.set(NegativeCache.key(name, bodyHash), {
      name,
      bodyHash,
      reason,
      expiresAt: this.#now() + ttlMs,
    });
  }
}
