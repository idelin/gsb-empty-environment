import { randomBytes } from "node:crypto";

export const DEFAULT_NONCE_TTL_SECONDS = 300;

export type Clock = () => number;

export interface NonceRecord {
  /** Opaque nonce value that was returned to the holder. */
  value: string;
  /** Epoch milliseconds at which the nonce was issued. */
  issuedAtMs: number;
  /** Epoch milliseconds after which the nonce is no longer valid. */
  expiresAtMs: number;
  /** Epoch milliseconds at which the nonce was consumed, if applicable. */
  usedAtMs?: number;
}

export interface NonceStoreOptions {
  /** Nonce lifetime in seconds. Defaults to 300 seconds. */
  nonceTtlSeconds?: number;
  /** Alias for nonceTtlSeconds. */
  ttlSeconds?: number;
  /** Nonce lifetime in milliseconds. Takes precedence over the second-based options. */
  nonceTtlMs?: number;
  /** Alias for nonceTtlMs. */
  ttlMs?: number;
  /**
   * Clock returning epoch milliseconds. Useful for tests and for sharing a
   * clock with the proof validator. Defaults to Date.now.
   */
  now?: Clock | number;
}

/**
 * Minimal store contract needed by the proof validator. Implementations may
 * wrap Redis or another shared store; the consume operation must be atomic.
 */
export interface NonceStoreLike {
  issueNonce(): string;
  getNonce(nonce: string): NonceRecord | undefined;
  consumeNonce(nonce: string, atMs?: number): boolean;
  currentTime(): number;
}

function normalizeNow(now: Clock | number | undefined): Clock {
  if (typeof now === "number") {
    if (!Number.isFinite(now)) {
      throw new TypeError("now must be a finite epoch-millisecond value");
    }
    return () => now;
  }
  if (now === undefined) {
    return Date.now;
  }
  if (typeof now !== "function") {
    throw new TypeError("now must be a function returning epoch milliseconds");
  }
  return () => {
    const value = now();
    if (!Number.isFinite(value)) {
      throw new TypeError("now() must return a finite epoch-millisecond value");
    }
    return value;
  };
}

function resolveTtlMs(options: NonceStoreOptions): number {
  const ttlMs =
    options.nonceTtlMs ??
    options.ttlMs ??
    (options.nonceTtlSeconds ?? options.ttlSeconds ?? DEFAULT_NONCE_TTL_SECONDS) * 1000;

  if (!Number.isFinite(ttlMs) || ttlMs <= 0) {
    throw new RangeError("nonce TTL must be a positive, finite duration");
  }
  return ttlMs;
}

function cloneRecord(record: NonceRecord): NonceRecord {
  return { ...record };
}

/**
 * In-memory nonce store with single-process atomic single-use semantics.
 *
 * The random nonce is kept server-side. A proof is accepted only if the exact
 * nonce is present, unused, and unexpired. Consumption marks the record rather
 * than deleting it so replay attempts can be distinguished from unknown nonces.
 */
export class NonceStore implements NonceStoreLike {
  readonly #records = new Map<string, NonceRecord>();
  readonly #ttlMs: number;
  readonly #now: Clock;

  constructor(options: NonceStoreOptions = {}) {
    this.#ttlMs = resolveTtlMs(options);
    this.#now = normalizeNow(options.now);
  }

  currentTime(): number {
    return this.#now();
  }

  issueNonce(): string {
    const value = randomBytes(32).toString("base64url");
    const issuedAtMs = this.currentTime();

    this.#records.set(value, {
      value,
      issuedAtMs,
      expiresAtMs: issuedAtMs + this.#ttlMs,
    });

    return value;
  }

  /** Alias for issueNonce(). */
  issue(): string {
    return this.issueNonce();
  }

  getNonce(nonce: string): NonceRecord | undefined {
    const record = this.#records.get(nonce);
    return record === undefined ? undefined : cloneRecord(record);
  }

  /** Alias for getNonce(). */
  get(nonce: string): NonceRecord | undefined {
    return this.getNonce(nonce);
  }

  /**
   * Atomically consumes a nonce if it exists, has not been used, and has not
   * expired. The atMs argument lets a validator use the same instant for all
   * time checks.
   */
  consumeNonce(nonce: string, atMs: number = this.currentTime()): boolean {
    const record = this.#records.get(nonce);
    if (record === undefined) {
      return false;
    }
    if (record.usedAtMs !== undefined || atMs >= record.expiresAtMs) {
      return false;
    }

    record.usedAtMs = atMs;
    return true;
  }

  /** Alias for consumeNonce(). */
  consume(nonce: string, atMs?: number): boolean {
    return this.consumeNonce(nonce, atMs);
  }

  hasNonce(nonce: string): boolean {
    return this.#records.has(nonce);
  }

  /** Removes expired records. This is optional housekeeping, not authentication. */
  prune(atMs: number = this.currentTime()): number {
    let removed = 0;
    for (const [value, record] of this.#records) {
      if (atMs >= record.expiresAtMs) {
        this.#records.delete(value);
        removed += 1;
      }
    }
    return removed;
  }
}

/** Explicit alias for code that wants to make the storage choice obvious. */
export { NonceStore as InMemoryNonceStore };

export function createNonceStore(options: NonceStoreOptions = {}): NonceStore {
  return new NonceStore(options);
}
