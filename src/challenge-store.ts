import { ChallengeError } from "./errors.ts";
import { base64UrlEncode, timingSafeEqual, toBytes, type BytesInput } from "./bytes.ts";

export interface ConsumeChallengeOptions {
  /** Expected raw challenge bytes issued by the relying party. */
  readonly expected: BytesInput;
  /** Maximum age in milliseconds since issue; default 300_000 (5 minutes). */
  readonly ttlMs?: number;
  /** Epoch milliseconds at which consumption is evaluated. */
  readonly now?: number;
}

export interface IssuedChallenge {
  readonly key: string;
  readonly bytes: Uint8Array;
  readonly expiresAt: number;
}

export interface ChallengeStore {
  issue(challenge: BytesInput, ttlMs?: number): IssuedChallenge;
  consume(options: ConsumeChallengeOptions): void;
  size(): number;
  clear(): void;
}

interface StoredChallenge {
  bytes: Uint8Array;
  expiresAt: number;
}

const DEFAULT_TTL_MS = 5 * 60 * 1000;

/**
 * Single-process, memory-backed one-time challenge store.
 *
 * Consumption is a synchronous `Map.delete`, so even when two concurrent
 * async verification requests share an event loop exactly one of them can
 * observe the stored challenge: the second caller gets `CHALLENGE_REPLAY`.
 */
export class InMemoryChallengeStore implements ChallengeStore {
  private readonly entries = new Map<string, StoredChallenge>();
  private readonly clock: () => number;

  constructor(options: { now?: () => number } = {}) {
    this.clock = options.now ?? (() => Date.now());
  }

  issue(challenge: BytesInput, ttlMs: number = DEFAULT_TTL_MS): IssuedChallenge {
    const bytes = toBytes(challenge);
    if (bytes.length === 0) {
      throw new TypeError("Challenge must not be empty");
    }
    const key = base64UrlEncode(bytes);
    const expiresAt = this.clock() + ttlMs;
    this.entries.set(key, { bytes, expiresAt });
    return { key, bytes: new Uint8Array(bytes), expiresAt };
  }

  consume(options: ConsumeChallengeOptions): void {
    const expected = toBytes(options.expected);
    const key = base64UrlEncode(expected);
    const now = options.now ?? this.clock();

    const stored = this.entries.get(key);
    if (stored === undefined) {
      throw new ChallengeError(
        "CHALLENGE_REPLAY",
        "Challenge was already consumed or was never issued",
      );
    }

    if (stored.expiresAt <= now) {
      // Expired challenges are single-use as well: remove atomically, then fail.
      this.entries.delete(key);
      throw new ChallengeError("CHALLENGE_EXPIRED", "Challenge has expired");
    }
    if (!timingSafeEqual(stored.bytes, expected)) {
      throw new ChallengeError("CHALLENGE_MISMATCH", "Challenge content does not match the issued value");
    }
    // Atomic single-use guarantee: remove only once validity is established.
    this.entries.delete(key);
  }

  size(): number {
    return this.entries.size;
  }

  clear(): void {
    this.entries.clear();
  }
}
