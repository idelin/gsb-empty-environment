/**
 * A Range [start, end, mult] means: every integer index in [start, end]
 * (inclusive) occurs exactly `mult` times. Ranges in a list are kept
 * normalized: sorted by start, non-overlapping, and two adjacent ranges
 * with end + 1 === nextStart are merged iff they share the same mult.
 */
export type Range = [number, number, number];

export function normalizeRanges(ranges: readonly Range[]): Range[] {
  const sorted = ranges
    .filter(([, , mult]) => mult > 0)
    .filter(([start, end]) => start <= end)
    .map(([start, end, mult]): Range => [start, end, mult])
    .sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const out: Range[] = [];
  for (const [start, end, mult] of sorted) {
    const last = out[out.length - 1];
    if (last && last[2] === mult && start <= last[1] + 1) {
      last[1] = Math.max(last[1], end);
    } else {
      out.push([start, end, mult]);
    }
  }
  return out;
}

export function rangeCount(ranges: readonly Range[]): number {
  let total = 0;
  for (const [start, end, mult] of ranges) total += (end - start + 1) * mult;
  return total;
}

export function expandRanges(ranges: readonly Range[]): number[] {
  const out: number[] = [];
  for (const [start, end, mult] of ranges) {
    for (let index = start; index <= end; index++) {
      for (let k = 0; k < mult; k++) out.push(index);
    }
  }
  return out;
}

/** Insert one occurrence of `index`; returns a new normalized list. */
export function addIndex(ranges: readonly Range[], index: number): Range[] {
  const next: Range[] = [];
  let found = false;
  for (const [start, end, mult] of ranges) {
    if (!found && index >= start && index <= end) {
      found = true;
      if (start <= index - 1) next.push([start, index - 1, mult]);
      next.push([index, index, mult + 1]);
      if (index + 1 <= end) next.push([index + 1, end, mult]);
    } else {
      next.push([start, end, mult]);
    }
  }
  if (!found) next.push([index, index, 1]);
  return normalizeRanges(next);
}

/**
 * Remove one occurrence of `index`.
 * Returns the new normalized list, or null if the index is not present.
 */
export function removeIndex(
  ranges: readonly Range[],
  index: number,
): Range[] | null {
  let found = false;
  const next: Range[] = [];
  for (const [start, end, mult] of ranges) {
    if (!found && index >= start && index <= end) {
      found = true;
      if (start <= index - 1) next.push([start, index - 1, mult]);
      if (mult - 1 > 0) next.push([index, index, mult - 1]);
      if (index + 1 <= end) next.push([index + 1, end, mult]);
    } else {
      next.push([start, end, mult]);
    }
  }
  if (!found) return null;
  return normalizeRanges(next);
}

/** Count occurrences of a single index across the ranges. */
export function countIndex(ranges: readonly Range[], index: number): number {
  for (const [start, end, mult] of ranges) {
    if (index >= start && index <= end) return mult;
  }
  return 0;
}
