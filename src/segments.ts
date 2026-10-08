/**
 * Gap-delimited segments: maximal runs of consecutive valid points.
 * Missing values (null / NaN) split the series; a polyline must never be
 * drawn across a segment boundary.
 */
import type { Segment } from './types.ts';

/** Build segments from scratch. O(n). */
export function computeSegments(isValid: (i: number) => boolean, n: number): Segment[] {
  const out: Segment[] = [];
  let i = 0;
  let id = 0;
  while (i < n) {
    if (!isValid(i)) {
      i++;
      continue;
    }
    const start = i;
    while (i < n && isValid(i)) i++;
    out.push({ id: id++, start, end: i - 1 });
  }
  return out;
}

/**
 * Incrementally repair segments after the values in index range [lo, hi]
 * changed. Existing segments touching the changed window (including immediate
 * neighbours, which may merge or split) are rescanned; everything further
 * away is untouched. O(k + m) where k is the rescanned length and m the
 * number of removed segments.
 */
export function resegment(
  segments: Segment[],
  isValid: (i: number) => boolean,
  n: number,
  lo: number,
  hi: number,
  nextId: () => number,
): Segment[] {
  let a = lo;
  let b = hi;
  const kept: Segment[] = [];
  for (const seg of segments) {
    // A segment is affected if it intersects [lo-1, hi+1]: adjacency matters
    // because a new valid point can merge two old segments into one.
    if (seg.end >= lo - 1 && seg.start <= hi + 1) {
      a = Math.min(a, seg.start);
      b = Math.max(b, seg.end);
    } else {
      kept.push(seg);
    }
  }
  a = Math.max(0, a);
  b = Math.min(n - 1, b);
  const fresh: Segment[] = [];
  let i = a;
  while (i <= b) {
    if (!isValid(i)) {
      i++;
      continue;
    }
    const start = i;
    while (i <= b && isValid(i)) i++;
    fresh.push({ id: nextId(), start, end: i - 1 });
  }
  // Merge kept + fresh back into start-sorted order.
  const out: Segment[] = [];
  let p = 0;
  let q = 0;
  while (p < kept.length || q < fresh.length) {
    if (q >= fresh.length || (p < kept.length && kept[p].start < fresh[q].start)) {
      out.push(kept[p++]);
    } else {
      out.push(fresh[q++]);
    }
  }
  return out;
}

/** Binary search: the segment containing index, or undefined. */
export function segmentAt(segments: Segment[], index: number): Segment | undefined {
  let lo = 0;
  let hi = segments.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >>> 1;
    const seg = segments[mid];
    if (index < seg.start) hi = mid - 1;
    else if (index > seg.end) lo = mid + 1;
    else return seg;
  }
  return undefined;
}
