export type IntervalTuple = [number, number];

/**
 * Sorted, disjoint, merge-adjacent set of half-open byte intervals [start, end).
 * Tracks which byte ranges of a file version are durably stored and trusted.
 */
export class IntervalSet {
  private ranges: IntervalTuple[] = [];

  static from(ranges: Iterable<IntervalTuple>): IntervalSet {
    const set = new IntervalSet();
    for (const [start, end] of ranges) set.add(start, end);
    return set;
  }

  add(start: number, end: number): void {
    if (end <= start) return;
    const merged: IntervalTuple[] = [];
    let nextStart = start;
    let nextEnd = end;
    let inserted = false;
    for (const [rangeStart, rangeEnd] of this.ranges) {
      if (rangeEnd < nextStart) {
        merged.push([rangeStart, rangeEnd]);
        continue;
      }
      if (rangeStart > nextEnd) {
        if (!inserted) {
          merged.push([nextStart, nextEnd]);
          inserted = true;
        }
        merged.push([rangeStart, rangeEnd]);
        continue;
      }
      nextStart = Math.min(nextStart, rangeStart);
      nextEnd = Math.max(nextEnd, rangeEnd);
    }
    if (!inserted) merged.push([nextStart, nextEnd]);
    this.ranges = merged;
  }

  /** Portions of [start, end) already covered by this set, clipped to the query range. */
  intersections(start: number, end: number): IntervalTuple[] {
    const result: IntervalTuple[] = [];
    for (const [rangeStart, rangeEnd] of this.ranges) {
      if (rangeEnd <= start) continue;
      if (rangeStart >= end) break;
      result.push([Math.max(rangeStart, start), Math.min(rangeEnd, end)]);
    }
    return result;
  }

  /** Portions of [start, end) NOT yet covered by this set. */
  complement(start: number, end: number): IntervalTuple[] {
    const gaps: IntervalTuple[] = [];
    let cursor = start;
    for (const [rangeStart, rangeEnd] of this.ranges) {
      if (rangeEnd <= cursor) continue;
      if (rangeStart >= end) break;
      if (rangeStart > cursor) gaps.push([cursor, Math.min(rangeStart, end)]);
      cursor = Math.max(cursor, rangeEnd);
      if (cursor >= end) break;
    }
    if (cursor < end) gaps.push([cursor, end]);
    return gaps;
  }

  isComplete(size: number): boolean {
    if (size === 0) return true;
    return (
      this.ranges.length === 1 &&
      this.ranges[0][0] === 0 &&
      this.ranges[0][1] === size
    );
  }

  coveredBytes(): number {
    let total = 0;
    for (const [start, end] of this.ranges) total += end - start;
    return total;
  }

  toArray(): IntervalTuple[] {
    return this.ranges.map(([start, end]) => [start, end] as IntervalTuple);
  }
}
