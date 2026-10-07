/**
 * Text analysis: longest substring with at least k pairwise
 * non-overlapping occurrences.
 *
 * Semantics:
 * - An "occurrence" of a substring is any start index (overlaps allowed).
 * - A set of occurrences is "non-overlapping" when their half-open
 *   intervals [start, start + length) are pairwise disjoint.
 * - The raw occurrence count and the selectable non-overlapping count are
 *   different notions: "aa" occurs 3 times in "aaaa" (starts 0, 1, 2) but
 *   at most 2 of them can be selected without overlap (starts 0 and 2).
 * - Ranking: longer substring wins; ties are broken by UTF-8 byte order of
 *   the substring, then by the ascending list of start positions.
 * - The reported `starts` are the lexicographically smallest list of k
 *   pairwise non-overlapping occurrence positions (earliest possible).
 * - Empty input (or no qualifying substring) yields `null`.
 */

export interface NonOverlappingRepeat {
  /** The winning substring. */
  readonly substring: string;
  /** Ascending start indices of k pairwise non-overlapping occurrences. */
  readonly starts: readonly number[];
}

const utf8Encoder = new TextEncoder();

/** Compare two strings by their UTF-8 byte sequences. */
export function compareUtf8Bytes(a: string, b: string): number {
  const bytesA = utf8Encoder.encode(a);
  const bytesB = utf8Encoder.encode(b);
  const shared = Math.min(bytesA.length, bytesB.length);
  for (let i = 0; i < shared; i++) {
    const diff = bytesA[i]! - bytesB[i]!;
    if (diff !== 0) return diff < 0 ? -1 : 1;
  }
  return bytesA.length - bytesB.length;
}

/** All start indices where `sub` occurs in `text`, overlaps included. */
export function occurrenceStarts(text: string, sub: string): number[] {
  if (sub.length === 0) {
    throw new RangeError("sub must not be empty");
  }
  const starts: number[] = [];
  let from = 0;
  for (;;) {
    const hit = text.indexOf(sub, from);
    if (hit < 0) return starts;
    starts.push(hit);
    from = hit + 1;
  }
}

/**
 * Greedily select pairwise non-overlapping intervals of the given length
 * from ascending start positions. For equal-length intervals this yields
 * the maximum number of intervals, and the lexicographically smallest
 * start list among maximum selections. Stops early once `limit` are chosen.
 */
export function selectNonOverlapping(
  starts: readonly number[],
  length: number,
  limit?: number,
): number[] {
  const chosen: number[] = [];
  for (const start of starts) {
    if (chosen.length === 0 || start >= chosen[chosen.length - 1]! + length) {
      chosen.push(start);
      if (limit !== undefined && chosen.length >= limit) break;
    }
  }
  return chosen;
}

/** Canonical maximum set of pairwise non-overlapping occurrence starts. */
export function maxNonOverlappingStarts(text: string, sub: string): number[] {
  return selectNonOverlapping(occurrenceStarts(text, sub), sub.length);
}

function startsBySubstring(text: string, length: number): Map<string, number[]> {
  const map = new Map<string, number[]>();
  for (let i = 0; i + length <= text.length; i++) {
    const sub = text.substring(i, i + length);
    const bucket = map.get(sub);
    if (bucket) {
      bucket.push(i);
    } else {
      map.set(sub, [i]);
    }
  }
  return map;
}

/**
 * Longest substring of `text` with at least `k` pairwise non-overlapping
 * occurrences, or `null` when the text is empty or nothing qualifies.
 */
export function longestNonOverlappingRepeat(
  text: string,
  k: number,
): NonOverlappingRepeat | null {
  if (!Number.isInteger(k) || k < 1) {
    throw new RangeError("k must be a positive integer");
  }
  if (text.length === 0) {
    return null;
  }

  const hasQualifying = (length: number): boolean => {
    for (const starts of startsBySubstring(text, length).values()) {
      if (
        starts.length >= k &&
        selectNonOverlapping(starts, length, k).length >= k
      ) {
        return true;
      }
    }
    return false;
  };

  // Qualification is monotone in length: a qualifying substring of length L
  // has a qualifying prefix of every shorter length, so binary search works.
  let lo = 1;
  let hi = text.length;
  while (lo < hi) {
    const mid = Math.floor((lo + hi + 1) / 2);
    if (hasQualifying(mid)) {
      lo = mid;
    } else {
      hi = mid - 1;
    }
  }
  if (!hasQualifying(lo)) {
    return null;
  }

  let best: NonOverlappingRepeat | null = null;
  for (const [sub, starts] of startsBySubstring(text, lo)) {
    if (starts.length < k) continue;
    const chosen = selectNonOverlapping(starts, lo, k);
    if (chosen.length < k) continue;
    if (best === null || compareUtf8Bytes(sub, best.substring) < 0) {
      best = { substring: sub, starts: chosen };
    }
  }
  return best;
}
