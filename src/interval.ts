// Closed integer intervals [lo, hi]. Open/closed boundary semantics of the
// source comparisons are resolved at normalization time (e.g. `x < 5` becomes
// [min, 4]), so overlap tests here are simple and exact: [1,5] and [5,9]
// overlap at 5, while [1,4] and [5,9] are disjoint.

export interface Interval {
  lo: number;
  hi: number;
}

export function isEmpty(i: Interval): boolean {
  return i.lo > i.hi;
}

export function intersectInterval(a: Interval, b: Interval): Interval {
  return { lo: Math.max(a.lo, b.lo), hi: Math.min(a.hi, b.hi) };
}

/** a \ b, as 0..2 disjoint intervals. */
export function subtractInterval(a: Interval, b: Interval): Interval[] {
  const inter = intersectInterval(a, b);
  if (isEmpty(inter)) return [a];
  const out: Interval[] = [];
  if (a.lo < inter.lo) out.push({ lo: a.lo, hi: inter.lo - 1 });
  if (inter.hi < a.hi) out.push({ lo: inter.hi + 1, hi: a.hi });
  return out;
}

/** dom \ a, as 0..2 disjoint intervals. */
export function complementInterval(a: Interval, dom: Interval): Interval[] {
  return subtractInterval(dom, a);
}
