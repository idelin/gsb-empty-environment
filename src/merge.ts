// Merge per-segment hit intervals of one object into disjoint intervals,
// keeping provenance (which segment produced which sub-interval).
//
// Two intervals merge when they overlap or touch: next.start <= cur.end.
// With continuous rational time this also merges half-open neighbours such
// as [0, 5) and [5, 10) into [0, 10), which is exactly the desired
// "disjoint union" semantics for a trajectory crossing segment boundaries.

import { fracCompare } from './fraction.js';
import type { HitSource, MergedInterval } from './intersect.js';

export function mergeIntervals(sources: HitSource[]): MergedInterval[] {
  const sorted = sources.slice().sort((a, b) => {
    const c = fracCompare(a.interval.start, b.interval.start);
    if (c !== 0) {
      return c;
    }
    return fracCompare(a.interval.end, b.interval.end);
  });
  const out: MergedInterval[] = [];
  for (const s of sorted) {
    const last = out.length > 0 ? out[out.length - 1] : null;
    if (last !== null && fracCompare(s.interval.start, last.end) <= 0) {
      const cmp = fracCompare(s.interval.end, last.end);
      if (cmp > 0) {
        last.end = s.interval.end;
        last.endInclusive = s.interval.endInclusive;
      } else if (cmp === 0) {
        last.endInclusive = last.endInclusive || s.interval.endInclusive;
      }
      last.sources.push(s);
    } else {
      out.push({
        start: s.interval.start,
        end: s.interval.end,
        endInclusive: s.interval.endInclusive,
        sources: [s],
      });
    }
  }
  return out;
}
