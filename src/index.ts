/**
 * Text analysis: longest substring with at least k pairwise non-overlapping
 * occurrences.
 *
 * Ordering used to pick the answer among equally long candidates:
 *   1. longer substring wins;
 *   2. smaller in byte order wins (see `compareByteOrder`);
 *   3. smaller selected-start list wins (lexicographic, shorter-is-smaller
 *      on a common prefix). This last key is a formality: two distinct
 *   strings never compare equal in byte order.
 *
 * "Byte order" is defined over the string's UTF-16 code units, i.e. the
 * same order as JavaScript's native `<` on strings. For ASCII text this
 * coincides with UTF-8 byte order.
 */

export interface NonOverlappingRepeat {
  /** The winning substring. */
  substring: string;
  /**
   * Start indices of the selected pairwise non-overlapping occurrences:
   * the lexicographically smallest maximum-cardinality selection
   * (equivalently, the greedy earliest-start selection).
   */
  starts: number[];
  /** Total number of occurrences, overlaps included. */
  occurrenceCount: number;
  /** Maximum number of pairwise non-overlapping occurrences selectable. */
  maxNonOverlapping: number;
}

/** All start indices at which `pattern` occurs in `text` (overlaps included). */
export function findOccurrences(text: string, pattern: string): number[] {
  const starts: number[] = [];
  if (pattern.length === 0 || pattern.length > text.length) return starts;
  for (let i = 0; i + pattern.length <= text.length; i++) {
    if (text.startsWith(pattern, i)) starts.push(i);
  }
  return starts;
}

/**
 * Given occurrence starts (ascending) of a length-`length` substring, returns
 * the greedy earliest-start pairwise non-overlapping selection. For
 * equal-length intervals this is a maximum-cardinality selection and the
 * lexicographically smallest one among those.
 */
export function selectNonOverlapping(
  starts: readonly number[],
  length: number,
): number[] {
  const selected: number[] = [];
  let nextFree = 0;
  for (const start of starts) {
    if (selected.length === 0 || start >= nextFree) {
      selected.push(start);
      nextFree = start + length;
    }
  }
  return selected;
}

/**
 * Byte-order comparison over UTF-16 code units (same order as `<` on
 * strings). Returns a negative/zero/positive number.
 */
export function compareByteOrder(a: string, b: string): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const ca = a.charCodeAt(i);
    const cb = b.charCodeAt(i);
    if (ca !== cb) return ca - cb;
  }
  return a.length - b.length;
}

function compareNumberLists(a: readonly number[], b: readonly number[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

interface Candidate {
  substring: string;
  starts: number[];
  occurrenceCount: number;
}

/**
 * Finds the longest substring of `text` that admits at least `k` pairwise
 * non-overlapping occurrences, breaking ties by byte order and then by the
 * selected-start list.
 *
 * Note the distinction between `occurrenceCount` and `maxNonOverlapping`:
 * e.g. "aa" occurs 3 times in "aaaa" (starts 0, 1, 2) but only 2 of those
 * can be kept non-overlapping, so it does not qualify for k = 3.
 *
 * Returns `null` for empty input or when no substring qualifies.
 *
 * Runs in O(n^2 log n) time and O(n^2) space for a text of length n:
 * feasibility is monotone in the candidate length (any prefix of a feasible
 * substring is feasible), so the maximum length is found by binary search.
 */
export function longestNonOverlappingRepeat(
  text: string,
  k: number,
): NonOverlappingRepeat | null {
  if (!Number.isInteger(k) || k < 1) {
    throw new RangeError("k must be a positive integer");
  }
  if (text.length === 0) return null;

  const feasibleAtLength = (length: number): Candidate[] => {
    const startsByValue = new Map<string, number[]>();
    for (let i = 0; i + length <= text.length; i++) {
      const slice = text.slice(i, i + length);
      const list = startsByValue.get(slice);
      if (list) list.push(i);
      else startsByValue.set(slice, [i]);
    }
    const candidates: Candidate[] = [];
    for (const [substring, starts] of startsByValue) {
      const selected = selectNonOverlapping(starts, length);
      if (selected.length >= k) {
        candidates.push({
          substring,
          starts: selected,
          occurrenceCount: starts.length,
        });
      }
    }
    return candidates;
  };

  let lo = 1;
  let hi = text.length;
  let bestLength = 0;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (feasibleAtLength(mid).length > 0) {
      bestLength = mid;
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  if (bestLength === 0) return null;

  const candidates = feasibleAtLength(bestLength);
  candidates.sort(
    (a, b) =>
      compareByteOrder(a.substring, b.substring) ||
      compareNumberLists(a.starts, b.starts),
  );
  const top = candidates[0];
  return {
    substring: top.substring,
    starts: top.starts,
    occurrenceCount: top.occurrenceCount,
    maxNonOverlapping: top.starts.length,
  };
}
