import {
  compare,
  merge,
  tick,
  type VectorClock,
} from "./vector-clock.ts";

export interface Version<V> {
  clock: VectorClock;
  value: V;
}

/** One version of one key in flight between replicas. */
export interface Message<V> {
  origin: string;
  key: string;
  version: Version<V>;
}

/**
 * A single replica of a versioned key-value store.
 *
 * - Every write is stamped with a vector clock.
 * - A stored version is discarded once a strictly dominating version exists.
 * - Concurrent versions are all retained until an explicit `resolve`.
 * - `resolve` only covers the versions the caller has seen; versions the
 *   caller never observed stay concurrent with the resolution and survive.
 */
export class Replica<V> {
  readonly id: string;
  private clock: VectorClock = {};
  private readonly data = new Map<string, Version<V>[]>();

  constructor(id: string) {
    this.id = id;
  }

  /** Local blind write. Dominates exactly what this replica already knows. */
  put(key: string, value: V): Version<V> {
    this.clock = tick(this.clock, this.id);
    const version: Version<V> = { clock: { ...this.clock }, value };
    this.addVersion(key, version);
    return version;
  }

  /**
   * Explicit conflict resolution. The new version's clock covers every
   * clock in `seen` (defaults to all versions of `key` currently stored),
   * so it dominates precisely those versions and nothing the caller has
   * never observed.
   */
  resolve(key: string, value: V, seen?: VectorClock[]): Version<V> {
    const covering =
      seen ?? (this.data.get(key) ?? []).map((version) => version.clock);
    this.clock = tick(merge(this.clock, ...covering), this.id);
    const version: Version<V> = { clock: { ...this.clock }, value };
    this.addVersion(key, version);
    return version;
  }

  /** All retained versions of a key (concurrent siblings included). */
  get(key: string): Version<V>[] {
    return (this.data.get(key) ?? []).map((version) => ({
      clock: { ...version.clock },
      value: version.value,
    }));
  }

  values(key: string): V[] {
    return this.get(key).map((version) => version.value);
  }

  keys(): string[] {
    return [...this.data.keys()].sort();
  }

  /** Export every retained version as messages for anti-entropy. */
  emit(): Message<V>[] {
    const messages: Message<V>[] = [];
    for (const [key, versions] of this.data) {
      for (const version of versions) {
        messages.push({
          origin: this.id,
          key,
          version: { clock: { ...version.clock }, value: version.value },
        });
      }
    }
    return messages;
  }

  /**
   * Apply one incoming message. Idempotent: duplicates and versions already
   * dominated by local state are dropped, so delivery order never matters.
   */
  receive(message: Message<V>): void {
    this.clock = merge(this.clock, message.version.clock);
    this.addVersion(message.key, {
      clock: { ...message.version.clock },
      value: message.version.value,
    });
  }

  private addVersion(key: string, version: Version<V>): void {
    const versions = this.data.get(key) ?? [];
    for (const existing of versions) {
      const ordering = compare(existing.clock, version.clock);
      if (ordering === "equal" || ordering === "after") {
        this.data.set(key, versions);
        return;
      }
    }
    const kept = versions.filter(
      (existing) => compare(version.clock, existing.clock) !== "after",
    );
    kept.push(version);
    this.data.set(key, kept);
  }
}
