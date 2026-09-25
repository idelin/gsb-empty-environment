/** Integer interval algebra over the (unbounded) integer line. */

export interface Interval {
  readonly lo: number; // inclusive lower bound, may be -Infinity
  readonly hi: number; // inclusive upper bound, may be Infinity
}

export type CmpOp = "<" | "<=" | ">" | ">=" | "==" | "!=";

/** Sort and merge overlapping or integer-adjacent intervals. */
export function normalize(intervals: readonly Interval[]): Interval[] {
  const sorted = intervals
    .filter((i) => i.lo <= i.hi)
    .map((i) => ({ lo: i.lo, hi: i.hi }))
    .sort((a, b) => a.lo - b.lo || a.hi - b.hi);
  const out: Interval[] = [];
  for (const iv of sorted) {
    const last = out[out.length - 1];
    // integers: [0,1] and [2,3] are adjacent and merge into [0,3]
    if (last !== undefined && iv.lo <= last.hi + 1) {
      last.hi = Math.max(last.hi, iv.hi);
    } else {
      out.push(iv);
    }
  }
  return out;
}

export function intersect(a: readonly Interval[], b: readonly Interval[]): Interval[] {
  const xs = normalize(a);
  const ys = normalize(b);
  const out: Interval[] = [];
  let i = 0;
  let j = 0;
  while (i < xs.length && j < ys.length) {
    const lo = Math.max(xs[i].lo, ys[j].lo);
    const hi = Math.min(xs[i].hi, ys[j].hi);
    if (lo <= hi) out.push({ lo, hi });
    if (xs[i].hi < ys[j].hi) i += 1;
    else j += 1;
  }
  return out;
}

export function union(a: readonly Interval[], b: readonly Interval[]): Interval[] {
  return normalize([...a, ...b]);
}

/** Complement within the integers; unknown-at-missing semantics is unaffected. */
export function complement(intervals: readonly Interval[]): Interval[] {
  const out: Interval[] = [];
  let cursor = -Infinity;
  for (const iv of normalize(intervals)) {
    if (iv.lo > cursor) out.push({ lo: cursor, hi: iv.lo - 1 });
    cursor = iv.hi + 1;
    if (cursor === Infinity) return out;
  }
  if (cursor < Infinity) out.push({ lo: cursor, hi: Infinity });
  return out;
}

/** `intervals` must be normalized. */
export function contains(intervals: readonly Interval[], v: number): boolean {
  for (const iv of intervals) {
    if (v < iv.lo) return false;
    if (v <= iv.hi) return true;
  }
  return false;
}

export function coversDomain(
  intervals: readonly Interval[],
  min: number,
  max: number,
): boolean {
  const n = normalize(intervals);
  return n.length === 1 && n[0].lo <= min && n[0].hi >= max;
}

export function cmpToIntervals(op: CmpOp, v: number): Interval[] {
  switch (op) {
    case "<":
      return [{ lo: -Infinity, hi: v - 1 }];
    case "<=":
      return [{ lo: -Infinity, hi: v }];
    case ">":
      return [{ lo: v + 1, hi: Infinity }];
    case ">=":
      return [{ lo: v, hi: Infinity }];
    case "==":
      return [{ lo: v, hi: v }];
    case "!=":
      return [
        { lo: -Infinity, hi: v - 1 },
        { lo: v + 1, hi: Infinity },
      ];
  }
}

/** Finite set of integers -> exact union of unit-run intervals. */
export function setToIntervals(values: readonly number[]): Interval[] {
  const sorted = [...new Set(values)].sort((a, b) => a - b);
  return normalize(sorted.map((v) => ({ lo: v, hi: v })));
}
