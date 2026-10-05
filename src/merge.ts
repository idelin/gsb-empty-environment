import type { MergedInterval, SegmentHit } from "./types.ts";

/**
 * Merge per-segment hit intervals of one object into disjoint union
 * components, preserving per-segment provenance in `sources`.
 *
 * Hits are left-closed, so a hit starting exactly at the previous
 * component's (open or closed) right end still connects: [a,p) + [p,b)
 * union to [a,b). Components in the output are sorted and separated by
 * strictly positive gaps.
 */
export function mergeHits(hits: SegmentHit[]): MergedInterval[] {
  const sorted = [...hits].sort(
    (a, b) =>
      a.interval.lo.cmp(b.interval.lo) || a.interval.hi.cmp(b.interval.hi),
  );
  const out: MergedInterval[] = [];
  for (const hit of sorted) {
    const source = { segmentId: hit.segmentId, interval: hit.interval };
    const last = out[out.length - 1];
    if (last && hit.interval.lo.lte(last.hi)) {
      if (hit.interval.hi.gt(last.hi)) {
        last.hi = hit.interval.hi;
        last.hiOpen = hit.interval.hiOpen;
      } else if (hit.interval.hi.eq(last.hi)) {
        // Point hi is included iff at least one component includes it.
        last.hiOpen = last.hiOpen && hit.interval.hiOpen;
      }
      last.sources.push(source);
    } else {
      out.push({
        lo: hit.interval.lo,
        hi: hit.interval.hi,
        hiOpen: hit.interval.hiOpen,
        sources: [source],
      });
    }
  }
  return out;
}
