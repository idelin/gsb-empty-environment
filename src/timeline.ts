/**
 * Piecewise-linear time mapping from explicit external time to local
 * animation time.
 *
 * The map is a list of segments. Each segment anchors a local time at an
 * external start time and advances local time at a constant rate
 * (`local = startLocal + (t - startTime) * rate`) until the next segment
 * begins. A paused animation is simply a segment with rate 0.
 *
 * Operations (`cut` / `reset`) discard every segment starting at or after
 * the operation time before appending the new segment. This gives two
 * important properties:
 *  - Multiple operations at the same timestamp compose in call order; the
 *    last one wins for the mapping from that timestamp onward, while still
 *    observing the local time produced by earlier same-timestamp ops.
 *  - Zero-length segments (same start time) never corrupt the mapping.
 */
export interface Segment {
  /** External (explicit) time at which this segment becomes active. */
  readonly startTime: number;
  /** Local animation time at `startTime`. */
  readonly startLocal: number;
  /** Local milliseconds per external millisecond (0 while paused). */
  readonly rate: number;
}

export class PiecewiseTimeMap {
  private segments: Segment[] = [];

  constructor(initial: Segment) {
    this.segments.push(initial);
  }

  get length(): number {
    return this.segments.length;
  }

  get last(): Segment {
    return this.segments[this.segments.length - 1];
  }

  at(index: number): Segment {
    return this.segments[index];
  }

  /** Maps an external time to local animation time. */
  map(t: number): number {
    let lo = 0;
    let hi = this.segments.length - 1;
    let idx = 0;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (this.segments[mid].startTime <= t) {
        idx = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    const s = this.segments[idx];
    return s.startLocal + (t - s.startTime) * s.rate;
  }

  /**
   * Returns the external time at which the mapping reaches `local`,
   * searching the most recent segments first. Returns NaN if the local
   * time is never reached.
   */
  timeAtLocal(local: number): number {
    for (let i = this.segments.length - 1; i >= 0; i--) {
      const s = this.segments[i];
      if (s.rate === 0) {
        if (local === s.startLocal) return s.startTime;
        continue;
      }
      const t = s.startTime + (local - s.startLocal) / s.rate;
      const end = i + 1 < this.segments.length ? this.segments[i + 1].startTime : Infinity;
      if (t >= s.startTime && t < end) return t;
    }
    return NaN;
  }

  /**
   * Anchors the current local time at external time `t` and continues with
   * `rate`. Local time is continuous across the cut (no jumps).
   */
  cut(t: number, rate: number): void {
    const local = this.map(t);
    this.truncate(t);
    this.segments.push({ startTime: t, startLocal: local, rate });
  }

  /** Anchors an explicit local time at external time `t` (seek). */
  reset(t: number, local: number, rate: number): void {
    this.truncate(t);
    this.segments.push({ startTime: t, startLocal: local, rate });
  }

  private truncate(t: number): void {
    while (this.segments.length > 0 && this.segments[this.segments.length - 1].startTime >= t) {
      this.segments.pop();
    }
  }
}
