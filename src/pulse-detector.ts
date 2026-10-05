export interface PulseDetectorConfig {
  /** Enter an event when value >= highThreshold. */
  highThreshold: number;
  /** Tentatively exit an event when value < lowThreshold. Must be <= highThreshold (equal is allowed). */
  lowThreshold: number;
  /** Max duration of a single dip below lowThreshold that can be bridged (merged) into the surrounding event. */
  maxGap: number;
  /** Max cumulative bridged-gap duration allowed within one event. */
  maxTotalGap: number;
}

export interface PulseEvent {
  readonly start: number;
  /** End timestamp; null while the event is still open. */
  readonly end: number | null;
  readonly peak: number;
  readonly peakTime: number;
  /** Trapezoidal integral of the signal over [start, end]. */
  readonly integral: number;
  /** Cumulative duration of bridged gaps inside this event. */
  readonly totalGap: number;
  /** false for an event that was still open when the stream ended (see flush). */
  readonly complete: boolean;
}

export interface CorrectionResult {
  /** Previously finalized events that no longer exist after the correction. */
  readonly retracted: PulseEvent[];
  /** Newly finalized events produced by the correction. */
  readonly emitted: PulseEvent[];
}

interface Sample {
  t: number;
  v: number;
}

interface OpenEvent {
  start: number;
  peak: number;
  peakTime: number;
  integral: number;
  totalGap: number;
}

type State = 'idle' | 'active' | 'gap';

function eventKey(e: PulseEvent): string {
  return `${e.start}|${e.end}`;
}

export class PulseDetector {
  private readonly cfg: PulseDetectorConfig;
  private samples: Sample[] = [];
  private emitted: PulseEvent[] = [];

  private state: State = 'idle';
  private open: OpenEvent | null = null;
  private gapStart = 0;
  private integralAtGapStart = 0;
  private lastT = 0;
  private lastV = 0;

  constructor(config: PulseDetectorConfig) {
    if (!Number.isFinite(config.highThreshold) || !Number.isFinite(config.lowThreshold)) {
      throw new Error('thresholds must be finite numbers');
    }
    if (config.lowThreshold > config.highThreshold) {
      throw new Error('lowThreshold must be <= highThreshold');
    }
    if (!(config.maxGap >= 0) || !(config.maxTotalGap >= 0)) {
      throw new Error('maxGap and maxTotalGap must be >= 0');
    }
    this.cfg = { ...config };
  }

  /**
   * Stream one sample. Timestamps must be strictly increasing.
   * Returns events finalized by this sample (usually empty).
   */
  feed(t: number, v: number): PulseEvent[] {
    if (!Number.isFinite(t) || !Number.isFinite(v)) {
      throw new Error('feed() requires finite t and v');
    }
    const last = this.samples[this.samples.length - 1];
    if (last && t <= last.t) {
      throw new Error('feed() requires strictly increasing timestamps; use correct() for late or revised data');
    }
    this.samples.push({ t, v });
    const before = this.emitted.length;
    this.step(t, v);
    return this.emitted.slice(before);
  }

  /**
   * Apply a late correction: replace the sample at t, or insert one if none
   * exists. Recomputes events and reports which finalized events were
   * retracted and which were newly emitted (a correction may split an
   * existing event into several).
   */
  correct(t: number, v: number): CorrectionResult {
    if (!Number.isFinite(t) || !Number.isFinite(v)) {
      throw new Error('correct() requires finite t and v');
    }
    const existing = this.samples.findIndex((s) => s.t === t);
    if (existing >= 0) {
      this.samples[existing] = { t, v };
    } else {
      let pos = this.samples.findIndex((s) => s.t > t);
      if (pos < 0) pos = this.samples.length;
      this.samples.splice(pos, 0, { t, v });
    }
    const previous = this.emitted;
    this.recompute();
    const current = this.emitted;
    const currentKeys = new Set(current.map(eventKey));
    const previousKeys = new Set(previous.map(eventKey));
    return {
      retracted: previous.filter((e) => !currentKeys.has(eventKey(e))),
      emitted: current.filter((e) => !previousKeys.has(eventKey(e))),
    };
  }

  /**
   * End the stream. If an event is still open it is finalized with
   * complete=false and returned; otherwise returns null.
   */
  flush(): PulseEvent | null {
    if (!this.open) return null;
    this.closeOpen(false);
    return this.emitted[this.emitted.length - 1]!;
  }

  /** Snapshot of the currently open event, or null. */
  pending(): PulseEvent | null {
    if (!this.open) return null;
    const inGap = this.state === 'gap';
    return {
      start: this.open.start,
      end: null,
      peak: this.open.peak,
      peakTime: this.open.peakTime,
      integral: inGap ? this.integralAtGapStart : this.open.integral,
      totalGap: this.open.totalGap,
      complete: false,
    };
  }

  /** All finalized events plus the open one (if any). */
  events(): PulseEvent[] {
    const all = this.emitted.slice();
    const p = this.pending();
    if (p) all.push(p);
    return all;
  }

  private recompute(): void {
    this.state = 'idle';
    this.open = null;
    this.gapStart = 0;
    this.integralAtGapStart = 0;
    this.lastT = 0;
    this.lastV = 0;
    this.emitted = [];
    for (const s of this.samples) {
      this.step(s.t, s.v);
    }
  }

  private step(t: number, v: number): void {
    const { highThreshold, lowThreshold, maxGap, maxTotalGap } = this.cfg;
    switch (this.state) {
      case 'idle':
        if (v >= highThreshold) {
          this.open = { start: t, peak: v, peakTime: t, integral: 0, totalGap: 0 };
          this.state = 'active';
        }
        break;
      case 'active': {
        const ev = this.open!;
        ev.integral += ((this.lastV + v) / 2) * (t - this.lastT);
        if (v > ev.peak) {
          ev.peak = v;
          ev.peakTime = t;
        }
        if (v < lowThreshold) {
          this.state = 'gap';
          this.gapStart = t;
          this.integralAtGapStart = ev.integral;
        }
        break;
      }
      case 'gap': {
        const ev = this.open!;
        if (v >= highThreshold) {
          const gapDur = t - this.gapStart;
          if (gapDur > maxGap || ev.totalGap + gapDur > maxTotalGap) {
            this.closeOpen(true);
            this.open = { start: t, peak: v, peakTime: t, integral: 0, totalGap: 0 };
            this.state = 'active';
          } else {
            ev.totalGap += gapDur;
            ev.integral += ((this.lastV + v) / 2) * (t - this.lastT);
            if (v > ev.peak) {
              ev.peak = v;
              ev.peakTime = t;
            }
            this.state = 'active';
          }
        } else if (t - this.gapStart > maxGap || ev.totalGap + (t - this.gapStart) > maxTotalGap) {
          this.closeOpen(true);
          this.state = 'idle';
        } else {
          ev.integral += ((this.lastV + v) / 2) * (t - this.lastT);
        }
        break;
      }
    }
    this.lastT = t;
    this.lastV = v;
  }

  private closeOpen(complete: boolean): void {
    const ev = this.open!;
    const inGap = this.state === 'gap';
    this.emitted.push({
      start: ev.start,
      end: inGap ? this.gapStart : this.lastT,
      peak: ev.peak,
      peakTime: ev.peakTime,
      integral: inGap ? this.integralAtGapStart : ev.integral,
      totalGap: ev.totalGap,
      complete,
    });
    this.open = null;
  }
}
