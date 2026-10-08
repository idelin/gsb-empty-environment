/**
 * Asynchronous zoom scheduler. Every zoom level queries the same shared
 * segmented summary (the series' summary tree + segment list); results are
 * cached per viewport. A mutation bumps the series version, and a task that
 * finishes late must never publish a result computed from an older version
 * over newer data.
 */
import { queryViewport } from './viewport.ts';
import type { TimeSeries } from './series.ts';
import type { SampledPoint, Viewport } from './types.ts';

export type ZoomStatus = 'fresh' | 'stale';

export interface ZoomResult {
  status: ZoomStatus;
  /** The series version the points were computed from. */
  version: number;
  points: SampledPoint[];
}

interface CacheEntry {
  version: number;
  points: SampledPoint[];
}

export class ZoomScheduler {
  private readonly series: TimeSeries;
  private readonly schedule: (task: () => void) => void;
  private readonly cache = new Map<string, CacheEntry>();
  private readonly pending = new Map<string, Promise<ZoomResult>>();

  /**
   * @param schedule how to run the computation asynchronously; defaults to a
   *        macrotask. Tests inject a manual queue to control completion order.
   */
  constructor(series: TimeSeries, schedule: (task: () => void) => void = (t) => setTimeout(t, 0)) {
    this.series = series;
    this.schedule = schedule;
  }

  private keyOf(vp: Viewport): string {
    return `${vp.t0}|${vp.t1}|${vp.width}`;
  }

  /** Cached points for this viewport, only if they match the current version. */
  cached(vp: Viewport): SampledPoint[] | undefined {
    const entry = this.cache.get(this.keyOf(vp));
    return entry && entry.version === this.series.version ? entry.points : undefined;
  }

  /** Drop every cache entry computed from an older data version. */
  prune(): void {
    for (const [key, entry] of this.cache) {
      if (entry.version !== this.series.version) this.cache.delete(key);
    }
  }

  /**
   * Compute (or reuse) the downsampled points for one zoom level.
   * The result is published to the cache only when the series version is
   * unchanged since the computation started; otherwise it is reported as
   * 'stale' and discarded, so an old task can never overwrite newer data.
   */
  request(vp: Viewport): Promise<ZoomResult> {
    const key = this.keyOf(vp);
    const version = this.series.version;
    const hit = this.cache.get(key);
    if (hit && hit.version === version) {
      return Promise.resolve({ status: 'fresh', version, points: hit.points });
    }
    const running = this.pending.get(key);
    if (running) {
      // A task for this viewport is already in flight; its own version guard
      // decides whether its result may be published.
      return running;
    }
    const promise = new Promise<ZoomResult>((resolve) => {
      this.schedule(() => {
        const points = queryViewport(this.series, vp.t0, vp.t1, vp.width);
        resolve({ status: 'fresh', version, points });
      });
    }).then((result) => {
      this.pending.delete(key);
      if (result.version !== this.series.version) {
        // Data changed while we were computing: never publish.
        return { status: 'stale', version: result.version, points: result.points };
      }
      this.cache.set(key, { version: result.version, points: result.points });
      return result;
    });
    this.pending.set(key, promise);
    return promise;
  }
}
