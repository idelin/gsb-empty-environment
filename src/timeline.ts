import { KeyframeAnimation } from './animation.ts';
import type { AnimationEvent } from './animation.ts';

export type Animatable = KeyframeAnimation | Timeline;

export interface TimelineChild {
  item: Animatable;
  start: number;
  rate: number;
}

export interface PauseSegment {
  at: number;
  duration: number;
}

export interface DeliveredEvent extends AnimationEvent {
  direction: 1 | -1;
  path: string;
}

function durationOf(item: Animatable): number {
  return item instanceof Timeline ? item.duration : item.totalDuration;
}

// A timeline composes children (animations or nested timelines) on a local
// time axis. Every mapping below is a pure function of input time: nothing is
// accumulated incrementally, so seeking and sequential advancement produce
// identical values, and time scaling composes exactly.
export class Timeline {
  readonly children: TimelineChild[] = [];
  readonly pauses: PauseSegment[] = [];
  reducedMotion = false;

  private markers: { time: number; name: string; data?: unknown }[] = [];

  add(item: Animatable, start: number, rate = 1): this {
    if (rate === 0) throw new Error('rate must be non-zero');
    if (rate < 0 && !Number.isFinite(durationOf(item))) {
      throw new Error('negative-rate children must have finite duration');
    }
    this.children.push({ item, start, rate });
    return this;
  }

  // A pause segment [at, at + duration) in input-time coordinates: the
  // playhead holds while input time crosses the segment.
  addPause(at: number, duration: number): this {
    if (!(duration > 0)) throw new Error('pause duration must be > 0');
    for (const q of this.pauses) {
      if (at < q.at + q.duration && q.at < at + duration) {
        throw new Error('pause segments must not overlap');
      }
    }
    this.pauses.push({ at, duration });
    this.pauses.sort((a, b) => a.at - b.at);
    return this;
  }

  addMarker(time: number, name: string, data?: unknown): this {
    this.markers.push({ time, name, data });
    return this;
  }

  private pausedBefore(t: number): number {
    let acc = 0;
    for (const p of this.pauses) acc += Math.min(Math.max(t - p.at, 0), p.duration);
    return acc;
  }

  // Input time -> playhead time. Constant across pause segments.
  playhead(t: number): number {
    return t - this.pausedBefore(t);
  }

  // Playhead time -> input time. A pause plateau resolves to its entry time.
  inputTimeForPlayhead(ph: number): number {
    let acc = 0;
    for (const p of this.pauses) {
      const hold = p.at - acc;
      if (ph < hold) return ph + acc;
      if (ph === hold) return p.at;
      acc += p.duration;
    }
    return ph + acc;
  }

  get duration(): number {
    let end = 0;
    for (const c of this.children) {
      const d = durationOf(c.item);
      if (!Number.isFinite(d)) return Infinity;
      end = Math.max(end, c.start + d / Math.abs(c.rate));
    }
    return this.inputTimeForPlayhead(end);
  }

  // Child-local time for a playhead time. Negative rate plays the child from
  // its end towards its start. Outside the child's window the first/last
  // frame holds (the value is clamped, never extrapolated).
  childTime(child: TimelineChild, ph: number): number {
    const d = durationOf(child.item);
    const raw = (ph - child.start) * child.rate;
    const local = child.rate >= 0 ? raw : d + raw;
    return Math.min(Math.max(local, 0), d);
  }

  valueAt(t: number): Record<string, number> {
    const ph = this.playhead(t);
    const out: Record<string, number> = {};
    for (const c of this.children) {
      Object.assign(out, c.item.valueAt(this.childTime(c, ph)));
    }
    return out;
  }

  // Collects events crossed between input times t0 and t1, in travel order.
  // Forward travel delivers (t0, t1], backward travel delivers [t1, t0);
  // events exactly on a shared boundary are delivered exactly once.
  collectEvents(t0: number, t1: number, path = 'timeline'): DeliveredEvent[] {
    if (this.reducedMotion) return [];
    const ph0 = this.playhead(t0);
    const ph1 = this.playhead(t1);
    if (ph0 === ph1) return [];
    const forward = ph1 > ph0;
    const lo = Math.min(ph0, ph1);
    const hi = Math.max(ph0, ph1);
    const inInterval = (te: number) => (forward ? te > lo && te <= hi : te >= lo && te < hi);

    const out: DeliveredEvent[] = [];
    for (const m of this.markers) {
      if (inInterval(m.time)) {
        out.push({ ...m, direction: forward ? 1 : -1, path });
      }
    }
    this.children.forEach((c, index) => {
      const childPath = `${path}/child[${index}]`;
      const c0 = this.childTime(c, ph0);
      const c1 = this.childTime(c, ph1);
      const sub: DeliveredEvent[] =
        c.item instanceof Timeline
          ? c.item.collectEvents(c0, c1, childPath)
          : c.item
              .eventsInInterval(c0, c1)
              .map((e) => ({ ...e, direction: (c1 > c0 ? 1 : -1) as 1 | -1, path: childPath }));
      const d = durationOf(c.item);
      for (const e of sub) {
        // Map the child-local event time back to this timeline's playhead
        // time so events from all children order consistently.
        const ph = c.rate >= 0 ? c.start + e.time / c.rate : c.start + (e.time - d) / c.rate;
        out.push({ ...e, time: ph });
      }
    });
    out.sort((a, b) => (forward ? a.time - b.time : b.time - a.time));
    // Report times in input coordinates (the caller's coordinate system).
    for (const e of out) e.time = this.inputTimeForPlayhead(e.time);
    return out;
  }
}
