/**
 * Exhaustive reference implementation used to cross-check the library.
 * Everything here is deliberately naive: enumerate every substring, every
 * subset of its occurrence starts, and verify pairwise non-overlap directly.
 */

import {
  NonOverlappingRepeat,
  compareUtf8Bytes,
  occurrenceStarts,
} from "../src/index";

export function compareStartLists(a: readonly number[], b: readonly number[]): number {
  const shared = Math.min(a.length, b.length);
  for (let i = 0; i < shared; i++) {
    if (a[i]! !== b[i]!) return a[i]! - b[i]!;
  }
  return a.length - b.length;
}

function isPairwiseNonOverlapping(starts: readonly number[], length: number): boolean {
  for (let i = 0; i < starts.length; i++) {
    for (let j = i + 1; j < starts.length; j++) {
      const a = starts[i]!;
      const b = starts[j]!;
      if (Math.max(a, b) < Math.min(a, b) + length) return false;
    }
  }
  return true;
}

/** Maximum number of pairwise non-overlapping occurrences, by enumeration. */
export function bruteMaxNonOverlappingCount(
  starts: readonly number[],
  length: number,
): number {
  let best = 0;
  const total = 1 << starts.length;
  for (let mask = 1; mask < total; mask++) {
    const picked: number[] = [];
    for (let i = 0; i < starts.length; i++) {
      if (mask & (1 << i)) picked.push(starts[i]!);
    }
    if (picked.length > best && isPairwiseNonOverlapping(picked, length)) {
      best = picked.length;
    }
  }
  return best;
}

/** Lexicographically smallest pairwise non-overlapping k-subset, or null. */
export function bruteCanonicalStarts(
  starts: readonly number[],
  length: number,
  k: number,
): number[] | null {
  let best: number[] | null = null;
  const chosen: number[] = [];
  const visit = (index: number): void => {
    if (chosen.length === k) {
      if (best === null || compareStartLists(chosen, best) < 0) {
        best = [...chosen];
      }
      return;
    }
    for (let i = index; i < starts.length; i++) {
      const start = starts[i]!;
      if (chosen.length === 0 || start >= chosen[chosen.length - 1]! + length) {
        chosen.push(start);
        visit(i + 1);
        chosen.pop();
      }
    }
  };
  visit(0);
  return best;
}

/** Exhaustive counterpart of `longestNonOverlappingRepeat`. */
export function bruteForceLongest(text: string, k: number): NonOverlappingRepeat | null {
  if (k < 1) throw new RangeError("k must be a positive integer");
  for (let length = text.length; length >= 1; length--) {
    const seen = new Map<string, number[]>();
    for (let i = 0; i + length <= text.length; i++) {
      const sub = text.substring(i, i + length);
      if (!seen.has(sub)) seen.set(sub, occurrenceStarts(text, sub));
    }
    let bestAtLength: NonOverlappingRepeat | null = null;
    for (const [sub, starts] of seen) {
      const canonical = bruteCanonicalStarts(starts, length, k);
      if (canonical === null) continue;
      if (
        bestAtLength === null ||
        compareUtf8Bytes(sub, bestAtLength.substring) < 0 ||
        (sub === bestAtLength.substring &&
          compareStartLists(canonical, bestAtLength.starts) < 0)
      ) {
        bestAtLength = { substring: sub, starts: canonical };
      }
    }
    if (bestAtLength !== null) return bestAtLength;
  }
  return null;
}
