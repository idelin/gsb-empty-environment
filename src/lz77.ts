/**
 * Stage 1: LZ77 token extraction over a 32 KiB sliding window.
 *
 * A hash table maps 3-byte sequences to the most recent position; each
 * position keeps a chain of previous candidates (zlib-style head/prev
 * arrays). Matching uses lazy evaluation: a match is emitted only if the
 * next position cannot start a strictly longer one.
 */

import { MIN_MATCH, MAX_MATCH, WINDOW_SIZE } from "./tables.ts";

export interface Token {
  /** Literal byte value, or match length when `distance > 0`. */
  literalOrLength: number;
  /** 0 for a literal, otherwise the match distance (1..32768). */
  distance: number;
}

const HASH_BITS = 15;
const HASH_SIZE = 1 << HASH_BITS;
const HASH_MASK = HASH_SIZE - 1;
const WINDOW_MASK = WINDOW_SIZE - 1;
const NO_POS = -1;

/** Max hash-chain links traversed per position (speed/ratio trade-off). */
const MAX_CHAIN = 256;
/** Stop searching early once a match this good is found. */
const NICE_LENGTH = 128;
/** Reduce chain effort once the current best is already decent. */
const GOOD_LENGTH = 32;

export class Lz77Encoder {
  private readonly head = new Int32Array(HASH_SIZE).fill(NO_POS);
  private readonly prev = new Int32Array(WINDOW_SIZE).fill(NO_POS);

  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  private hashAt(pos: number): number {
    const d = this.data;
    // Multiplicative hash of the 3 bytes at `pos`.
    return (
      (((d[pos] << 10) ^ (d[pos + 1] << 5) ^ d[pos + 2]) * 0x9e3779b1) >>>
      (32 - HASH_BITS)
    ) & HASH_MASK;
  }

  private insert(pos: number): void {
    if (pos + MIN_MATCH > this.data.length) return;
    const h = this.hashAt(pos);
    this.prev[pos & WINDOW_MASK] = this.head[h];
    this.head[h] = pos;
  }

  /** Longest match at `pos` against previous positions; {length, distance}. */
  private findMatch(pos: number, prevLength: number): { length: number; distance: number } {
    const data = this.data;
    const maxLen = Math.min(MAX_MATCH, data.length - pos);
    if (maxLen < MIN_MATCH) return { length: 0, distance: 0 };
    // A deferred match at least this long always wins the lazy comparison.
    if (prevLength >= maxLen) return { length: 0, distance: 0 };

    let bestLen = Math.max(MIN_MATCH - 1, prevLength);
    let bestDist = 0;
    const limit = pos - WINDOW_SIZE;
    let chainLeft =
      prevLength >= GOOD_LENGTH ? MAX_CHAIN >> 2 : MAX_CHAIN;

    let cand = this.head[this.hashAt(pos)];
    while (cand !== NO_POS && cand >= limit && chainLeft-- > 0) {
      // Quick reject: the byte that would extend the best match must differ.
      if (data[cand + bestLen] === data[pos + bestLen]) {
        let len = 0;
        while (len < maxLen && data[cand + len] === data[pos + len]) len++;
        if (len > bestLen) {
          bestLen = len;
          bestDist = pos - cand;
          if (len >= NICE_LENGTH || len >= maxLen) break;
        }
      }
      cand = this.prev[cand & WINDOW_MASK];
    }

    if (bestDist === 0) return { length: 0, distance: 0 };
    return { length: bestLen, distance: bestDist };
  }

  /** Tokenize the whole input with lazy (one-step lookahead) matching. */
  tokenize(): Token[] {
    const tokens: Token[] = [];
    const n = this.data.length;
    let pos = 0;
    let pending = this.findMatch(0, MIN_MATCH - 1);

    while (pos < n) {
      const next =
        pos + 1 < n
          ? (this.insert(pos), this.findMatch(pos + 1, pending.length))
          : { length: 0, distance: 0 };

      if (pending.length >= MIN_MATCH && next.length <= pending.length) {
        // Emit the deferred match and register every covered position.
        tokens.push({ literalOrLength: pending.length, distance: pending.distance });
        const end = pos + pending.length;
        for (let p = pos + 1; p < end && p < n; p++) this.insert(p);
        pos = end;
        pending = pos < n ? this.findMatch(pos, MIN_MATCH - 1) : { length: 0, distance: 0 };
      } else {
        tokens.push({ literalOrLength: this.data[pos], distance: 0 });
        pos++;
        pending = next;
      }
    }
    return tokens;
  }
}

export function lz77Tokenize(data: Uint8Array): Token[] {
  return new Lz77Encoder(data).tokenize();
}
