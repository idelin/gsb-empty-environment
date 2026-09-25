import { Clock } from './clock.ts';
import { KeyframeAnimation } from './animation.ts';
import { Timeline } from './timeline.ts';
import type { DeliveredEvent } from './timeline.ts';

// Drives a root animation/timeline. Seeking and sequential ticking share the
// exact same advance path, so both observe identical property values and
// identical event streams for identical crossed intervals.
export class Player {
  timeScale = 1;
  onEvent: ((e: DeliveredEvent) => void) | null = null;
  readonly events: DeliveredEvent[] = [];

  private cursor = 0;
  private readonly root: Timeline | KeyframeAnimation;
  private readonly clock: Clock;

  constructor(root: Timeline | KeyframeAnimation, clock: Clock) {
    this.root = root;
    this.clock = clock;
  }

  get position(): number {
    return this.cursor;
  }

  private rootEvents(t0: number, t1: number): DeliveredEvent[] {
    if (this.root instanceof Timeline) return this.root.collectEvents(t0, t1);
    return this.root
      .eventsInInterval(t0, t1)
      .map((e) => ({ ...e, direction: (t1 > t0 ? 1 : -1) as 1 | -1, path: 'animation' }));
  }

  advanceTo(t: number): DeliveredEvent[] {
    const delivered = t === this.cursor ? [] : this.rootEvents(this.cursor, t);
    this.cursor = t;
    for (const e of delivered) {
      this.events.push(e);
      this.onEvent?.(e);
    }
    return delivered;
  }

  // Sequential advance driven by the clock. The root time is always computed
  // as clock time x timeScale in one exact multiplication, never accumulated.
  tick(): DeliveredEvent[] {
    return this.advanceTo(this.clock.now() * this.timeScale);
  }

  seek(t: number): DeliveredEvent[] {
    return this.advanceTo(t);
  }

  value(): Record<string, number> {
    return this.root.valueAt(this.cursor);
  }

  valueAt(t: number): Record<string, number> {
    return this.root.valueAt(t);
  }
}
