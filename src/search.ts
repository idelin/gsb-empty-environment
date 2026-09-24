/**
 * Literal search over ChunkedText with a bounded temporary buffer.
 *
 * The matcher is a streaming KMP automaton: it reads the text code unit by
 * code unit exactly once per scanned window and keeps only the O(m) failure
 * table (m = pattern length). It never concatenates chunks and never holds
 * more than O(1) text characters at a time, so patterns longer than a chunk
 * (or longer than several chunks) work without materializing the full text.
 */

import type { ChunkedText } from "./chunked-text.ts";

export function normalizePattern(pattern: string, caseSensitive: boolean): string {
  return caseSensitive ? pattern : pattern.toLowerCase();
}

function kmpFailure(p: string): number[] {
  const fail = new Array<number>(p.length).fill(0);
  let k = 0;
  for (let i = 1; i < p.length; i++) {
    while (k > 0 && p[i] !== p[k]) k = fail[k - 1];
    if (p[i] === p[k]) k++;
    fail[i] = k;
  }
  return fail;
}

/**
 * Yield start offsets s with lo <= s <= hi of every (possibly overlapping)
 * occurrence of `pattern` (already normalized) in `text`.
 *
 * Reads at most the code units in [lo, min(length, hi + m)) — i.e. the window
 * that can contain a reported match — and nothing else.
 *
 * Empty-pattern semantics: matches at every position lo..hi (inclusive),
 * capped at text.length, so a full-text empty search yields length+1 matches.
 */
export function* searchRange(
  text: ChunkedText,
  pattern: string,
  lower: boolean,
  lo: number,
  hi: number,
): Generator<number, void, undefined> {
  const m = pattern.length;
  const from = Math.max(0, lo);
  if (m === 0) {
    const last = Math.min(hi, text.length);
    for (let p = from; p <= last; p++) yield p;
    return;
  }
  const fail = kmpFailure(pattern);
  const end = Math.min(text.length, hi + m);
  let j = 0;
  for (let i = from; i < end; i++) {
    const c = text.charAt(i, lower);
    while (j > 0 && c !== pattern[j]) j = fail[j - 1];
    if (c === pattern[j]) j++;
    if (j === m) {
      const start = i - m + 1;
      if (start >= lo && start <= hi) yield start;
      j = fail[j - 1];
    }
  }
}

export interface FindOptions {
  /** UTF-16 offset to start from (default 0). If it lands inside a surrogate
   *  pair it is moved forward past the pair — offsets never split pairs. */
  from?: number;
  /** Case-exact matching (default true). */
  caseSensitive?: boolean;
}

/** Clamp a start offset so it never points into the middle of a surrogate pair. */
export function clampOffset(text: ChunkedText, offset: number): number {
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) {
    throw new RangeError(`offset ${offset} out of range [0, ${text.length}]`);
  }
  return text.splitsSurrogatePair(offset) ? offset + 1 : offset;
}

/** All (overlapping) match starts of `pattern` in `text`, ascending. */
export function findAll(
  text: ChunkedText,
  pattern: string,
  options: FindOptions = {},
): number[] {
  const caseSensitive = options.caseSensitive ?? true;
  const from = clampOffset(text, options.from ?? 0);
  const norm = normalizePattern(pattern, caseSensitive);
  return [...searchRange(text, norm, !caseSensitive, from, text.length)];
}
