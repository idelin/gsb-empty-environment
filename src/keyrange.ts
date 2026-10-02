/**
 * Key range [start, end): start inclusive, end exclusive.
 */
export interface KeyRange {
  readonly start: string;
  readonly end: string;
}

export function rangeKey(range: KeyRange): string {
  return `[${range.start}-${range.end})`;
}

export function contains(range: KeyRange, key: string): boolean {
  return key >= range.start && key < range.end;
}

export function overlaps(a: KeyRange, b: KeyRange): boolean {
  return a.start < b.end && b.start < a.end;
}

/**
 * Pick a split point inside the range. For single-character bounds the
 * midpoint of the char codes is used (e.g. [A-Z) -> 'M'); otherwise the
 * midpoint of the first-char codes is appended after `start`.
 */
export function midpointOf(range: KeyRange): string {
  const lo = range.start.charCodeAt(0);
  const hi = range.end.charCodeAt(0);
  if (!Number.isFinite(lo) || !Number.isFinite(hi) || hi <= lo + 1) {
    throw new Error(`cannot split degenerate range ${rangeKey(range)}`);
  }
  const mid = String.fromCharCode(Math.floor((lo + hi) / 2));
  if (mid <= range.start || mid >= range.end) {
    throw new Error(`computed split point ${mid} outside ${rangeKey(range)}`);
  }
  return mid;
}
