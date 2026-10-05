/**
 * ValSet: a set of domain values represented as sorted, disjoint, inclusive
 * intervals over the field's domain index space (enum value -> its position,
 * int value -> value - min). All condition kinds (range / enum set / negation)
 * compile down to this single representation.
 */
export type ValSet = [number, number][];

export function fullSet(size: number): ValSet {
  return [[0, size - 1]];
}

export function isEmpty(s: ValSet): boolean {
  return s.length === 0;
}

export function sizeOf(s: ValSet): number {
  let acc = 0;
  for (const [lo, hi] of s) acc += hi - lo + 1;
  return acc;
}

export function intersect(a: ValSet, b: ValSet): ValSet {
  const out: ValSet = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    const lo = Math.max(a[i][0], b[j][0]);
    const hi = Math.min(a[i][1], b[j][1]);
    if (lo <= hi) out.push([lo, hi]);
    if (a[i][1] < b[j][1]) i++;
    else j++;
  }
  return out;
}

export function subtract(a: ValSet, b: ValSet): ValSet {
  const out: ValSet = [];
  for (const [lo, hi] of a) {
    let cur = lo;
    for (const [blo, bhi] of b) {
      if (bhi < cur) continue;
      if (blo > hi) break;
      if (blo > cur) out.push([cur, blo - 1]);
      cur = Math.max(cur, bhi + 1);
      if (cur > hi) break;
    }
    if (cur <= hi) out.push([cur, hi]);
  }
  return out;
}

export function contains(s: ValSet, idx: number): boolean {
  return s.some(([lo, hi]) => lo <= idx && idx <= hi);
}

export function firstValue(s: ValSet): number {
  if (s.length === 0) throw new Error("firstValue of empty set");
  return s[0][0];
}

export function lastValue(s: ValSet): number {
  if (s.length === 0) throw new Error("lastValue of empty set");
  return s[s.length - 1][1];
}
