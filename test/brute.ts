/**
 * Independent brute-force reference for cross-checking the library:
 * enumerates every substring of the text, finds every occurrence by direct
 * scan, and enumerates every subset of occurrences to find the maximum
 * pairwise non-overlapping selection. Only suitable for short texts.
 */

export interface BruteResult {
  substring: string;
  starts: number[];
  occurrenceCount: number;
  maxNonOverlapping: number;
}

function lexCompare(a: readonly number[], b: readonly number[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

/** Exhaustive subset enumeration over occurrence intervals [start, start+length). */
export function bestSelectionExhaustive(
  starts: readonly number[],
  length: number,
): number[] {
  const m = starts.length;
  if (m > 24) throw new Error("text too long for exhaustive selection");
  let best: number[] = [];
  const total = 1 << m;
  for (let mask = 1; mask < total; mask++) {
    const selected: number[] = [];
    let ok = true;
    let previousStart = -1;
    for (let i = 0; i < m; i++) {
      if ((mask & (1 << i)) === 0) continue;
      if (previousStart >= 0 && starts[i] < previousStart + length) {
        ok = false;
        break;
      }
      selected.push(starts[i]);
      previousStart = starts[i];
    }
    if (!ok) continue;
    if (
      selected.length > best.length ||
      (selected.length === best.length && lexCompare(selected, best) < 0)
    ) {
      best = selected;
    }
  }
  return best;
}

function compareCandidates(a: BruteResult, b: BruteResult): number {
  if (a.substring.length !== b.substring.length) {
    return b.substring.length - a.substring.length; // longer wins
  }
  if (a.substring !== b.substring) {
    return a.substring < b.substring ? -1 : 1; // byte order (UTF-16 code units)
  }
  return lexCompare(a.starts, b.starts);
}

export function bruteForceLongestRepeat(text: string, k: number): BruteResult | null {
  if (text.length === 0) return null;
  let best: BruteResult | null = null;
  for (let length = 1; length <= text.length; length++) {
    for (let i = 0; i + length <= text.length; i++) {
      const substring = text.slice(i, i + length);
      const occurrences: number[] = [];
      for (let j = 0; j + length <= text.length; j++) {
        let match = true;
        for (let t = 0; t < length; t++) {
          if (text[j + t] !== substring[t]) {
            match = false;
            break;
          }
        }
        if (match) occurrences.push(j);
      }
      const selected = bestSelectionExhaustive(occurrences, length);
      if (selected.length < k) continue;
      const candidate: BruteResult = {
        substring,
        starts: selected,
        occurrenceCount: occurrences.length,
        maxNonOverlapping: selected.length,
      };
      if (best === null || compareCandidates(candidate, best) < 0) {
        best = candidate;
      }
    }
  }
  return best;
}
