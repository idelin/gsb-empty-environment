import { bucketColumn, buildBuckets, mergeBuckets } from './buckets.ts';
import { assembleSegment, visibleColumnRange } from './downsample.ts';
import { computeSegments } from './segments.ts';
import type {
  ColumnBucket,
  DataPoint,
  MaybeValue,
  SampledPoint,
  Segment,
} from './types.ts';

export interface Viewport {
  fromTime: number;
  toTime: number;
}

export interface EngineOptions {
  /** Column width in time units at zoom level 0; level k doubles it k times. */
  baseColumnWidth: number;
  /** Fixed grid origin shared by all zoom levels and viewports. Default 0. */
  origin?: number;
}

export interface ScheduledResult {
  /** Data version the result was computed from. */
  version: number;
  /** True when the data changed before this task completed: do not apply. */
  stale: boolean;
  level: number;
  result: SampledPoint[];
}

interface SegmentSummary {
  key: string;
  /** Lazily built per-zoom-level column buckets, shared by all viewports. */
  levels: Map<number, ColumnBucket[]>;
}

interface Snapshot {
  version: number;
  points: readonly DataPoint[];
  segments: readonly Segment[];
  summaries: ReadonlyMap<string, SegmentSummary>;
}

interface PendingTask extends Snapshot {
  level: number;
  viewport?: Viewport;
  includeEdgeNeighbors: boolean;
  resolve: (result: ScheduledResult) => void;
}

function segmentKey(segment: Segment): string {
  return `${segment.startIndex}:${segment.endIndex}`;
}

/**
 * Downsampling engine for one time series.
 *
 * - Zoom level k uses column width `baseColumnWidth * 2^k` on a fixed grid.
 * - Each segment owns a lazily built summary shared by all zoom levels:
 *   level 0 is computed from raw points, coarser levels are merged from the
 *   previous one, so a summary built for one zoom/viewport serves all others.
 * - `updateValues` performs a local edit: it bumps the data version,
 *   recomputes segment boundaries (breakpoints may move), and invalidates
 *   only the summaries of the segments the edit touched.
 * - `scheduleDownsample` captures an immutable snapshot and runs later via
 *   `flushPending`; a task whose version is no longer current resolves with
 *   `stale: true` and never overwrites a newer applied result.
 */
export class DownsampleEngine {
  /** Current data version; bumped by every mutation. */
  version = 0;

  private points: DataPoint[];
  private segments: Segment[];
  private summaries = new Map<string, SegmentSummary>();
  private readonly origin: number;
  private readonly baseColumnWidth: number;
  private pending: PendingTask[] = [];
  private latestResults = new Map<number, SampledPoint[]>();
  private latestResultVersions = new Map<number, number>();

  constructor(points: readonly DataPoint[], options: EngineOptions) {
    if (!(options.baseColumnWidth > 0)) throw new RangeError('baseColumnWidth must be > 0');
    this.baseColumnWidth = options.baseColumnWidth;
    this.origin = options.origin ?? 0;
    this.points = points.map((p) => ({ time: p.time, value: p.value }));
    this.segments = computeSegments(this.points);
    this.resegment(new Set());
  }

  columnWidth(level: number): number {
    return this.baseColumnWidth * 2 ** level;
  }

  get pendingTasks(): number {
    return this.pending.length;
  }

  /** Local in-place edit: overwrite values at [startIndex, startIndex + values.length). */
  updateValues(startIndex: number, values: readonly MaybeValue[]): void {
    if (values.length === 0) return;
    if (startIndex < 0 || startIndex + values.length > this.points.length) {
      throw new RangeError(`edit range ${startIndex}..${startIndex + values.length - 1} out of bounds`);
    }
    const next = this.points.slice();
    for (let i = 0; i < values.length; i++) {
      next[startIndex + i] = { time: next[startIndex + i].time, value: values[i] };
    }
    // Segments overlapping the edit may change extremes or breakpoints.
    const dirtyKeys = new Set<string>();
    const editEnd = startIndex + values.length;
    for (const seg of this.segments) {
      if (seg.startIndex < editEnd && startIndex < seg.endIndex) dirtyKeys.add(segmentKey(seg));
    }
    this.points = next;
    this.version++;
    this.segments = computeSegments(this.points);
    this.resegment(dirtyKeys);
  }

  /** Keep summaries of untouched, identically-bounded segments; drop the rest. */
  private resegment(dirtyKeys: ReadonlySet<string>): void {
    const previous = this.summaries;
    const next = new Map<string, SegmentSummary>();
    for (const segment of this.segments) {
      const key = segmentKey(segment);
      const kept = previous.get(key);
      if (kept && !dirtyKeys.has(key)) next.set(key, kept);
      else next.set(key, { key, levels: new Map() });
    }
    this.summaries = next;
  }

  /**
   * Buckets for one segment at one zoom level. Computes level 0 from raw
   * points and merges upwards, caching every intermediate level so all zoom
   * levels and viewports share the same segment summary.
   */
  private bucketsForLevel(
    points: readonly DataPoint[],
    segment: Segment,
    summary: SegmentSummary,
    level: number,
  ): ColumnBucket[] {
    const cached = summary.levels.get(level);
    if (cached) return cached;
    let baseLevel = -1;
    for (const existing of summary.levels.keys()) {
      if (existing < level && existing > baseLevel) baseLevel = existing;
    }
    let buckets: ColumnBucket[];
    if (baseLevel === -1) {
      buckets = buildBuckets(
        points, segment.startIndex, segment.endIndex, this.baseColumnWidth, this.origin,
      );
      summary.levels.set(0, buckets);
      baseLevel = 0;
    } else {
      buckets = summary.levels.get(baseLevel)!;
    }
    for (let l = baseLevel; l < level; l++) {
      buckets = mergeBuckets(buckets);
      summary.levels.set(l + 1, buckets);
    }
    return buckets;
  }

  private computeFrom(
    snapshot: Snapshot,
    level: number,
    viewport: Viewport | undefined,
    includeEdgeNeighbors: boolean,
  ): SampledPoint[] {
    const width = this.columnWidth(level);
    const [colMin, colMax] = viewport
      ? visibleColumnRange(viewport.fromTime, viewport.toTime, width, this.origin)
      : [-Infinity, Infinity];
    const columnOf = (time: number): number => bucketColumn(time, width, this.origin);
    const out: SampledPoint[] = [];
    for (const segment of snapshot.segments) {
      const summary = snapshot.summaries.get(segmentKey(segment));
      const buckets = summary
        ? this.bucketsForLevel(snapshot.points, segment, summary, level)
        : buildBuckets(snapshot.points, segment.startIndex, segment.endIndex, width, this.origin);
      const visible = colMin === -Infinity && colMax === Infinity
        ? buckets
        : buckets.filter((b) => b.column >= colMin && b.column <= colMax);
      out.push(...assembleSegment(snapshot.points, segment, visible, includeEdgeNeighbors, columnOf));
    }
    out.sort((a, b) => a.time - b.time || a.index - b.index);
    return out;
  }

  /** Synchronous downsample at a zoom level, optionally clipped to a viewport. */
  downsample(level: number, viewport?: Viewport, includeEdgeNeighbors = viewport !== undefined): SampledPoint[] {
    return this.computeFrom(this.snapshot(), level, viewport, includeEdgeNeighbors);
  }

  /**
   * Schedule an asynchronous zoom task on an immutable snapshot of the
   * current version. The returned promise resolves on `flushPending`; if the
   * data has since changed, the result is flagged stale and is not applied.
   */
  scheduleDownsample(
    level: number,
    viewport?: Viewport,
    includeEdgeNeighbors = viewport !== undefined,
  ): Promise<ScheduledResult> {
    const snapshot = this.snapshot();
    return new Promise<ScheduledResult>((resolve) => {
      this.pending.push({ ...snapshot, level, viewport, includeEdgeNeighbors, resolve });
    });
  }

  /**
   * Complete all pending tasks. `lifo` simulates out-of-order worker
   * completion; either way a stale task never overwrites a newer result.
   */
  flushPending(order: 'fifo' | 'lifo' = 'fifo'): void {
    const tasks = this.pending.splice(0);
    if (order === 'lifo') tasks.reverse();
    for (const task of tasks) {
      const result = this.computeFrom(
        task, task.level, task.viewport, task.includeEdgeNeighbors,
      );
      const stale = task.version !== this.version;
      if (!stale) {
        const applied = this.latestResultVersions.get(task.level);
        if (applied === undefined || task.version >= applied) {
          this.latestResults.set(task.level, result);
          this.latestResultVersions.set(task.level, task.version);
        }
      }
      task.resolve({ version: task.version, stale, level: task.level, result });
    }
  }

  /** Last non-stale asynchronously computed result for a zoom level. */
  getLatestResult(level: number): SampledPoint[] | undefined {
    return this.latestResults.get(level);
  }

  /** Test/debug aid: zoom levels currently cached in a segment's summary. */
  getCachedLevels(startIndex: number, endIndex: number): number[] | undefined {
    const summary = this.summaries.get(`${startIndex}:${endIndex}`);
    return summary ? [...summary.levels.keys()].sort((a, b) => a - b) : undefined;
  }

  private snapshot(): Snapshot {
    // points/segments/summaries are replaced (never mutated) on edits, so
    // capturing references freezes a consistent version for async tasks.
    return {
      version: this.version,
      points: this.points,
      segments: this.segments,
      summaries: this.summaries,
    };
  }
}
