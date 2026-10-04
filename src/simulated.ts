import { maxRat, minRat, Rat } from './rational.ts';
import type { Interval, IntervalProvider, RefineResult } from './sorter.ts';

export interface SimBehavior {
  /** True value; every emitted interval keeps containing it. */
  readonly trueValue: Rat;
  /** Return an exact certificate on the n-th refinement of this item. */
  readonly exactAfter?: number;
  /** Report `exhausted` once refinements exceed this count. */
  readonly exhaustAfter?: number;
  /** Report `cancelled` once refinements exceed this count. */
  readonly cancelAfter?: number;
  /** On this refinement count, deliberately break the provider contract. */
  readonly corruptAt?: number;
  readonly corruptWith?: 'widen' | 'stagnate' | 'exact-outside';
}

/**
 * Deterministic provider used for tests and demos. Each refinement shrinks the
 * interval to a quarter-width window around the true value (clamped to the
 * previous interval), so tightening is strict and monotone by construction.
 */
export class SimulatedProvider<I> implements IntervalProvider<I> {
  private readonly behaviors = new Map<I, SimBehavior>();
  private readonly intervals = new Map<I, Interval>();
  private readonly calls = new Map<I, number>();

  register(item: I, initial: Interval, behavior: SimBehavior): void {
    if (behavior.trueValue.lt(initial.lo) || behavior.trueValue.gt(initial.hi)) {
      throw new Error('true value must lie inside the initial interval');
    }
    this.behaviors.set(item, behavior);
    this.intervals.set(item, initial);
    this.calls.set(item, 0);
  }

  initialInterval(item: I): Interval {
    const interval = this.intervals.get(item);
    if (interval === undefined) throw new Error('unknown item');
    return interval;
  }

  refine(item: I): RefineResult {
    const behavior = this.behaviors.get(item);
    const current = this.intervals.get(item);
    if (behavior === undefined || current === undefined) throw new Error('unknown item');
    const n = (this.calls.get(item) ?? 0) + 1;
    this.calls.set(item, n);

    if (behavior.corruptAt === n) {
      switch (behavior.corruptWith) {
        case 'widen':
          return {
            kind: 'interval',
            interval: { lo: current.lo.sub(Rat.one()), hi: current.hi.add(Rat.one()) },
          };
        case 'exact-outside':
          return { kind: 'exact', value: current.hi.add(Rat.one()) };
        default:
          return { kind: 'interval', interval: current };
      }
    }
    if (behavior.cancelAfter !== undefined && n > behavior.cancelAfter) {
      return { kind: 'cancelled' };
    }
    if (behavior.exhaustAfter !== undefined && n > behavior.exhaustAfter) {
      return { kind: 'exhausted' };
    }
    if (behavior.exactAfter !== undefined && n >= behavior.exactAfter) {
      this.intervals.set(item, { lo: behavior.trueValue, hi: behavior.trueValue });
      return { kind: 'exact', value: behavior.trueValue };
    }

    const quarter = current.hi.sub(current.lo).div(Rat.of(4));
    const next: Interval = {
      lo: maxRat(current.lo, behavior.trueValue.sub(quarter)),
      hi: minRat(current.hi, behavior.trueValue.add(quarter)),
    };
    this.intervals.set(item, next);
    return { kind: 'interval', interval: next };
  }
}
