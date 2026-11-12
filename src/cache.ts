/**
 * MTA-STS policy cache. Entries are retained until their policy `max_age`
 * elapses; the disappearance of the DNS `_mta-sts` record does not evict an
 * entry and does not shorten its lifetime (RFC 8461 §7.2 / §10.1).
 */

import type { MtaStsPolicy } from "./policy.ts";

export interface CachedPolicy {
  policy: MtaStsPolicy;
  /** Absolute expiry time in epoch milliseconds. */
  expiresAt: number;
  /** id of the policy as advertised by the current/last _mta-sts record. */
  id: string;
}

export interface CacheSnapshot {
  [domain: string]: CachedPolicy;
}

export interface PolicyCache {
  get(domain: string, now: number): CachedPolicy | null;
  set(domain: string, id: string, policy: MtaStsPolicy, now: number): void;
  delete(domain: string): void;
  clear(): void;
  snapshot(): CacheSnapshot;
  load(snapshot: CacheSnapshot): void;
}

/** In-memory implementation keyed by lowercased domain. */
export class InMemoryPolicyCache implements PolicyCache {
  private readonly entries = new Map<string, CachedPolicy>();

  get(domain: string, now: number): CachedPolicy | null {
    const entry = this.entries.get(domain.toLowerCase());
    if (entry === undefined) {
      return null;
    }
    if (entry.expiresAt <= now) {
      this.entries.delete(domain.toLowerCase());
      return null;
    }
    return entry;
  }

  set(domain: string, id: string, policy: MtaStsPolicy, now: number): void {
    this.entries.set(domain.toLowerCase(), {
      policy,
      id,
      expiresAt: now + policy.maxAge * 1000,
    });
  }

  delete(domain: string): void {
    this.entries.delete(domain.toLowerCase());
  }

  clear(): void {
    this.entries.clear();
  }

  snapshot(): CacheSnapshot {
    return Object.fromEntries(this.entries.entries());
  }

  load(snapshot: CacheSnapshot): void {
    this.entries.clear();
    for (const [domain, entry] of Object.entries(snapshot)) {
      this.entries.set(domain.toLowerCase(), {
        id: entry.id,
        expiresAt: entry.expiresAt,
        policy: {
          mode: entry.policy.mode,
          maxAge: entry.policy.maxAge,
          mx: [...entry.policy.mx],
        },
      });
    }
  }
}
