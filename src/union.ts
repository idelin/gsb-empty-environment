/**
 * Multi-segment domains produced e.g. by division across zero. Segments are
 * kept sorted, disjoint and merged when overlapping. When the segment count
 * would exceed `maxSegments`, the union collapses to its convex hull and
 * records the conservative enclosure via `precisionLoss` + `note`.
 */

import type { Interval } from './interval.ts';
import { EMPTY, cmpLo, cmpHi, containsPoint, div, hull, interval, show } from './interval.ts';
import { Decimal } from './decimal.ts';

export const DEFAULT_MAX_SEGMENTS = 8;

export class IntervalUnion {
  readonly segments: readonly Interval[];
  readonly precisionLoss: boolean;
  readonly note: string | null;
  readonly maxSegments: number;

  private constructor(
    segments: Interval[],
    precisionLoss: boolean,
    note: string | null,
    maxSegments: number,
  ) {
    this.segments = segments;
    this.precisionLoss = precisionLoss;
    this.note = note;
    this.maxSegments = maxSegments;
  }

  static empty(maxSegments: number = DEFAULT_MAX_SEGMENTS): IntervalUnion {
    return new IntervalUnion([], false, null, maxSegments);
  }

  static fromInterval(iv: Interval, maxSegments: number = DEFAULT_MAX_SEGMENTS): IntervalUnion {
    return IntervalUnion.fromSegments([iv], maxSegments);
  }

  static fromSegments(segments: readonly Interval[], maxSegments: number = DEFAULT_MAX_SEGMENTS): IntervalUnion {
    const sorted = segments
      .filter((s) => s.kind !== 'empty')
      .slice()
      .sort((a, b) => {
        if (a.kind !== 'bounded' || b.kind !== 'bounded') return 0;
        return cmpLo(a.lo, b.lo) || cmpHi(a.hi, b.hi);
      }) as Extract<Interval, { kind: 'bounded' }>[];

    const merged: Interval[] = [];
    for (const seg of sorted) {
      const last = merged[merged.length - 1];
      if (last !== undefined && last.kind === 'bounded' && cmpLo(seg.lo, last.hi) <= 0) {
        const hi = cmpHi(seg.hi, last.hi) > 0 ? seg.hi : last.hi;
        merged[merged.length - 1] = interval(last.lo, hi);
      } else {
        merged.push(seg);
      }
    }

    if (merged.length > maxSegments) {
      let h: Interval = EMPTY;
      for (const s of merged) h = hull(h, s);
      const note =
        `segment count ${merged.length} exceeds limit ${maxSegments}; ` +
        `conservatively enclosed by hull ${show(h)} (precision loss)`;
      return new IntervalUnion([h], true, note, maxSegments);
    }
    return new IntervalUnion(merged, false, null, maxSegments);
  }

  /** Division lifted to unions; splitting across zero yields up to 2 rays per pair. */
  static div(
    a: Interval,
    b: Interval,
    scale: number,
    onZeroCross: 'reject' | 'split',
    maxSegments: number = DEFAULT_MAX_SEGMENTS,
  ): IntervalUnion {
    return IntervalUnion.fromSegments(div(a, b, scale, onZeroCross), maxSegments);
  }

  isEmpty(): boolean {
    return this.segments.length === 0;
  }

  containsPoint(p: Decimal): boolean {
    return this.segments.some((s) => containsPoint(s, p));
  }

  hull(): Interval {
    let h: Interval = EMPTY;
    for (const s of this.segments) h = hull(h, s);
    return h;
  }

  toString(): string {
    if (this.segments.length === 0) return 'EMPTY-UNION';
    const body = this.segments.map(show).join(' ∪ ');
    return this.precisionLoss ? `${body}  [precision loss: ${this.note}]` : body;
    }
}
