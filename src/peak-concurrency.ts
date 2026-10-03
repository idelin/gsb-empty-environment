export interface TimeSegment {
  start: number;
  end: number;
}

export interface PeakResult {
  /** Peak concurrency value (0 when no intervals are tracked). */
  value: number;
  /**
   * All maximal continuous half-open time segments [start, end) where the
   * concurrency equals the peak value, sorted by start time.
   */
  segments: TimeSegment[];
}

/**
 * Tracks device occupancy intervals and reports the peak concurrency together
 * with every continuous time segment where that peak occurs.
 *
 * Semantics:
 * - Intervals are half-open [start, end): an interval ending at t does not
 *   overlap one starting at t.
 * - Intervals may arrive in any order (an interval ending earlier may be
 *   reported after one starting later) and may be backfilled late.
 * - Intervals can be retracted by id; the peak is recomputed dynamically.
 */
export class PeakConcurrencyTracker {
  readonly #deltas = new Map<number, number>();
  readonly #intervals = new Map<string, TimeSegment>();

  get size(): number {
    return this.#intervals.size;
  }

  has(id: string): boolean {
    return this.#intervals.has(id);
  }

  /** Registers an occupancy interval and returns the resulting peak. */
  add(id: string, start: number, end: number): PeakResult {
    if (!Number.isFinite(start) || !Number.isFinite(end)) {
      throw new RangeError(`Interval bounds must be finite numbers, got [${start}, ${end})`);
    }
    if (start >= end) {
      throw new RangeError(`Interval start must be before end, got [${start}, ${end})`);
    }
    if (this.#intervals.has(id)) {
      throw new Error(`Duplicate interval id: ${id}`);
    }
    this.#intervals.set(id, { start, end });
    this.#bump(start, 1);
    this.#bump(end, -1);
    return this.peak();
  }

  /** Retracts a previously registered interval and returns the resulting peak. */
  remove(id: string): PeakResult {
    const interval = this.#intervals.get(id);
    if (!interval) {
      throw new Error(`Unknown interval id: ${id}`);
    }
    this.#intervals.delete(id);
    this.#bump(interval.start, -1);
    this.#bump(interval.end, 1);
    return this.peak();
  }

  /** Returns the currently tracked intervals, sorted by start time. */
  intervals(): Array<TimeSegment & { id: string }> {
    return [...this.#intervals.entries()]
      .map(([id, segment]) => ({ id, ...segment }))
      .sort((a, b) => a.start - b.start || a.end - b.end);
  }

  /**
   * Sweeps all interval endpoints and computes the peak concurrency plus every
   * maximal continuous segment where the concurrency equals that peak.
   */
  peak(): PeakResult {
    const points = [...this.#deltas.keys()].sort((a, b) => a - b);
    let value = 0;
    let current = 0;
    const segments: TimeSegment[] = [];

    for (let i = 0; i < points.length; i += 1) {
      current += this.#deltas.get(points[i])!;
      if (i + 1 >= points.length) {
        break;
      }
      const segment: TimeSegment = { start: points[i], end: points[i + 1] };
      if (current > value) {
        value = current;
        segments.length = 0;
        segments.push(segment);
      } else if (current === value && value > 0) {
        const last = segments[segments.length - 1];
        if (last && last.end === segment.start) {
          last.end = segment.end;
        } else {
          segments.push(segment);
        }
      }
    }

    return { value, segments };
  }

  #bump(point: number, delta: number): void {
    const next = (this.#deltas.get(point) ?? 0) + delta;
    if (next === 0) {
      this.#deltas.delete(point);
    } else {
      this.#deltas.set(point, next);
    }
  }
}
