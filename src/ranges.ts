/**
 * Discrete integer interval algebra over the extended line [-Infinity, +Infinity].
 * A predicate on one integer attribute is represented as a sorted list of
 * disjoint boxes [lo, hi] (integers; endpoints may be infinite).
 */
import type { Interval } from "./ast.ts";

export function normalizeRanges(ranges: Interval[]): Interval[] {
  const finite = ranges
    .filter((r) => Number.isFinite(r.lo) || Number.isFinite(r.hi) || r.lo === -Infinity)
    .filter((r) => r.lo <= r.hi)
    .map((r) => ({ lo: Math.ceil(r.lo), hi: Math.floor(r.hi) }))
    .sort((a, b) => a.lo - b.lo);
  const merged: Interval[] = [];
  for (const r of finite) {
    const last = merged[merged.length - 1];
    // Adjacent boxes on the integer line join too: [1,2] ∪ [3,4] = [1,4].
    if (last && r.lo <= last.hi + 1) {
      last.hi = Math.max(last.hi, r.hi);
    } else {
      merged.push({ ...r });
    }
  }
  return merged;
}

export function intersectRanges(a: Interval[], b: Interval[]): Interval[] {
  const out: Interval[] = [];
  for (const x of a) {
    for (const y of b) {
      const lo = Math.max(x.lo, y.lo);
      const hi = Math.min(x.hi, y.hi);
      if (lo <= hi) out.push({ lo, hi });
    }
  }
  return normalizeRanges(out);
}

export function unionRanges(a: Interval[], b: Interval[]): Interval[] {
  return normalizeRanges([...a, ...b]);
}

/** Complement on Z: the gaps plus both half-lines. */
export function complementRanges(ranges: Interval[]): Interval[] {
  const r = normalizeRanges(ranges);
  const out: Interval[] = [];
  if (r.length === 0 || r[0].lo !== -Infinity) {
    out.push({ lo: -Infinity, hi: r.length === 0 ? Infinity : r[0].lo - 1 });
  }
  for (let i = 0; i + 1 < r.length; i++) {
    out.push({ lo: r[i].hi + 1, hi: r[i + 1].lo - 1 });
  }
  const last = r[r.length - 1];
  if (last && last.hi !== Infinity) {
    out.push({ lo: last.hi + 1, hi: Infinity });
  }
  return normalizeRanges(out);
}

export function isFullLine(ranges: Interval[]): boolean {
  const r = normalizeRanges(ranges);
  return r.length === 1 && r[0].lo === -Infinity && r[0].hi === Infinity;
}

export function isEmpty(ranges: Interval[]): boolean {
  return normalizeRanges(ranges).length === 0;
}

export function rangesContain(ranges: Interval[], v: number): boolean {
  return ranges.some((r) => r.lo <= v && v <= r.hi);
}

export function renderRanges(ranges: Interval[]): string {
  const fmt = (r: Interval) => {
    const left = r.lo === -Infinity ? "(-∞" : `[${r.lo}`;
    const right = r.hi === Infinity ? "+∞)" : `${r.hi}]`;
    return `${left}..${right}`;
  };
  return ranges.map(fmt).join("∪");
}
