import type { Ticks } from './time.ts';
import type { TimelineEvent } from './events.ts';
import { inCrossingInterval } from './events.ts';
import type { AnimNode, PropertyValues } from './animation.ts';

/** Parent-frame interval [start, start+duration) during which a child freezes. */
export interface PauseSegment {
  start: Ticks;
  duration: Ticks;
}

export interface ChildSpec {
  child: AnimNode;
  /** Parent time at which the child's local time equals `startAt`. May be negative. */
  offset?: Ticks;
  /**
   * Composed playbackRate * timeScale for this edge. May be negative: the
   * child then plays backwards as parent time advances.
   */
  rate?: number;
  /** Child local time at `offset`. Defaults to 0 (rate >= 0) or child.duration. */
  startAt?: Ticks;
  /** Sorted, non-overlapping freeze intervals in the parent frame. */
  pauses?: PauseSegment[];
}

interface ChildEntry {
  child: AnimNode;
  offset: Ticks;
  rate: number;
  startAt: Ticks;
  pauses: PauseSegment[];
}

/**
 * A timeline composes children at offsets with per-edge rates and pause
 * segments. The child-time mapping is a pure function of parent time, so
 * arbitrary seeking and sequential advancement produce identical values.
 */
export class Timeline implements AnimNode {
  readonly id: string;
  private entries: ChildEntry[];
  /** Own events, expressed in this timeline's local frame. */
  private ownEvents: { name: string; at: Ticks }[];
  version: number;

  constructor(id: string) {
    this.id = id;
    this.entries = [];
    this.ownEvents = [];
    this.version = 1;
  }

  add(spec: ChildSpec): this {
    const rate = spec.rate ?? 1;
    const startAt = spec.startAt ?? (rate >= 0 ? 0 : spec.child.duration);
    const pauses = (spec.pauses ?? []).slice().sort((a, b) => a.start - b.start);
    this.entries.push({ child: spec.child, offset: spec.offset ?? 0, rate, startAt, pauses });
    return this;
  }

  addEvent(name: string, at: Ticks): this {
    this.ownEvents.push({ name, at });
    return this;
  }

  /** Signed total of pause time between parent times `a` and `b`. */
  private pausedBetween(e: ChildEntry, a: Ticks, b: Ticks): Ticks {
    const lo = Math.min(a, b);
    const hi = Math.max(a, b);
    let acc = 0;
    for (const p of e.pauses) {
      const overlap = Math.min(hi, p.start + p.duration) - Math.max(lo, p.start);
      if (overlap > 0) acc += overlap;
    }
    return b >= a ? acc : -acc;
  }

  /** Pure mapping parent local time -> child local time. */
  private mapChild(e: ChildEntry, p: Ticks): Ticks {
    const paused = this.pausedBetween(e, e.offset, p);
    const elapsed = p - e.offset - paused;
    return e.startAt + Math.round(elapsed * e.rate);
  }

  /**
   * Inverse of `mapChild` at the crossing point: the first parent time in
   * [pLo, pHi] at which the child reaches local time `c`. Exact integer
   * binary search on the monotonic mapping; used to express child events in
   * this timeline's frame.
   */
  private childParentTime(e: ChildEntry, c: Ticks, pLo: Ticks, pHi: Ticks): Ticks {
    const increasing = e.rate >= 0;
    let lo = pLo;
    let hi = pHi;
    while (lo < hi) {
      const mid = lo + Math.trunc((hi - lo) / 2);
      const v = this.mapChild(e, mid);
      if (increasing ? v < c : v > c) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  childLocalAt(spec: ChildSpec, p: Ticks): Ticks {
    const rate = spec.rate ?? 1;
    const startAt = spec.startAt ?? (rate >= 0 ? 0 : spec.child.duration);
    const offset = spec.offset ?? 0;
    const pauses = (spec.pauses ?? []).slice().sort((a, b) => a.start - b.start);
    const lo = Math.min(offset, p);
    const hi = Math.max(offset, p);
    let acc = 0;
    for (const seg of pauses) {
      const overlap = Math.min(hi, seg.start + seg.duration) - Math.max(lo, seg.start);
      if (overlap > 0) acc += overlap;
    }
    const paused = p >= offset ? acc : -acc;
    const elapsed = p - offset - paused;
    return startAt + Math.round(elapsed * rate);
  }

  get duration(): Ticks {
    let d = 0;
    for (const e of this.entries) {
      if (e.rate === 0) {
        d = Math.max(d, e.offset);
        continue;
      }
      const target = e.rate > 0 ? e.child.duration : 0;
      const need = (target - e.startAt) / e.rate;
      if (!Number.isFinite(need)) return Infinity;
      if (need <= 0) {
        d = Math.max(d, e.offset);
        continue;
      }
      // Parent time when the child finishes: offset + need + pauses swallowed
      // along the way (single pass, pauses sorted and non-overlapping).
      let end = e.offset + Math.ceil(need);
      for (const p of e.pauses) {
        if (p.start >= end) break;
        const overlap = Math.min(end, p.start + p.duration) - Math.max(e.offset, p.start);
        if (overlap > 0) end += overlap;
      }
      d = Math.max(d, end);
    }
    return d;
  }

  sampleInto(out: PropertyValues, local: Ticks, reduced: boolean): void {
    for (const e of this.entries) {
      e.child.sampleInto(out, this.mapChild(e, local), reduced);
    }
  }

  collectEvents(from: Ticks, to: Ticks, into: TimelineEvent[], path: string): void {
    if (from === to) return;
    for (const ev of this.ownEvents) {
      if (inCrossingInterval(ev.at, from, to)) {
        into.push({ name: ev.name, at: ev.at, version: this.version, path });
      }
    }
    const pLo = Math.min(from, to);
    const pHi = Math.max(from, to);
    for (const e of this.entries) {
      const childEvents: TimelineEvent[] = [];
      e.child.collectEvents(
        this.mapChild(e, from),
        this.mapChild(e, to),
        childEvents,
        `${path}/${e.child.id}`,
      );
      // Re-express child events in this timeline's frame so that delivery
      // ordering and boundary deduplication use one consistent clock.
      for (const ev of childEvents) {
        into.push({ ...ev, at: this.childParentTime(e, ev.at, pLo, pHi) });
      }
    }
  }
}
