/**
 * The mutable time series: timestamps, values with missing points, the
 * gap-delimited segment list, the shared summary tree, and a monotonically
 * increasing version used to invalidate stale asynchronous zoom results.
 */
import { computeSegments, resegment, segmentAt } from './segments.ts';
import { SummaryTree } from './tree.ts';
import type { NodeSummary } from './tree.ts';
import type { MaybeValue, Segment } from './types.ts';

function normalize(v: MaybeValue | undefined): number | null {
  if (v === null || v === undefined || Number.isNaN(v)) return null;
  return v;
}

export class TimeSeries {
  /** Non-decreasing timestamps (duplicates allowed; ties break by index). */
  readonly times: number[];
  /** Normalized values: a number, or null for a missing point. */
  readonly values: (number | null)[];
  /** Gap-delimited segments, sorted by start. Never connected across. */
  segments: Segment[];
  /**
   * Bumped on every mutation. Async zoom tasks capture it on start and must
   * refuse to publish their result when it no longer matches.
   */
  version: number;
  private tree: SummaryTree;
  private nextSegmentId: number;

  constructor(times: ArrayLike<number>, values: ArrayLike<MaybeValue | undefined>) {
    if (times.length !== values.length) {
      throw new RangeError('times and values must have the same length');
    }
    this.times = Array.from(times as ArrayLike<number>);
    for (let i = 1; i < this.times.length; i++) {
      if (this.times[i] < this.times[i - 1]) {
        throw new RangeError(`times must be non-decreasing (index ${i})`);
      }
    }
    this.values = Array.from(values as ArrayLike<MaybeValue | undefined>, normalize);
    this.segments = computeSegments((i) => this.isValid(i), this.times.length);
    this.nextSegmentId = this.segments.length;
    this.tree = new SummaryTree(this.values);
    this.version = 0;
  }

  get length(): number {
    return this.times.length;
  }

  isValid(i: number): boolean {
    return this.values[i] !== null;
  }

  /** The segment containing index, or undefined when index is missing. */
  segmentAt(index: number): Segment | undefined {
    return segmentAt(this.segments, index);
  }

  /** Segments that have at least one point with time in [t0, t1]. */
  segmentsIntersecting(t0: number, t1: number): Segment[] {
    return this.segments.filter(
      (seg) => this.times[seg.start] <= t1 && this.times[seg.end] >= t0,
    );
  }

  /** Summary-tree range query over valid points in [lo, hi] inclusive. */
  queryRange(lo: number, hi: number): NodeSummary {
    return this.tree.query(lo, hi);
  }

  /** Overwrite one point. Splits/merges segments and bumps the version. */
  setValue(index: number, value: MaybeValue | undefined): void {
    this.checkIndex(index);
    const next = normalize(value);
    if (next === this.values[index]) return;
    this.values[index] = next;
    this.tree.update(index);
    this.segments = resegment(
      this.segments,
      (i) => this.isValid(i),
      this.length,
      index,
      index,
      () => this.nextSegmentId++,
    );
    this.version++;
  }

  /** Overwrite a contiguous run of points with one version bump. */
  setValues(start: number, vals: ArrayLike<MaybeValue | undefined>): void {
    if (vals.length === 0) return;
    this.checkIndex(start);
    this.checkIndex(start + vals.length - 1);
    let changed = false;
    for (let k = 0; k < vals.length; k++) {
      const next = normalize(vals[k]);
      if (next !== this.values[start + k]) {
        this.values[start + k] = next;
        changed = true;
      }
    }
    if (!changed) return;
    for (let k = 0; k < vals.length; k++) this.tree.update(start + k);
    this.segments = resegment(
      this.segments,
      (i) => this.isValid(i),
      this.length,
      start,
      start + vals.length - 1,
      () => this.nextSegmentId++,
    );
    this.version++;
  }

  private checkIndex(index: number): void {
    if (!Number.isInteger(index) || index < 0 || index >= this.length) {
      throw new RangeError(`index out of range: ${index}`);
    }
  }
}
