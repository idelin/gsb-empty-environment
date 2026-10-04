import { Rat } from './rational.ts';

/** Closed rational interval [lo, hi]; the true value is guaranteed to lie inside. */
export interface Interval {
  readonly lo: Rat;
  readonly hi: Rat;
}

/** Result of one refinement request to the provider. */
export type RefineResult =
  | { readonly kind: 'interval'; readonly interval: Interval }
  | { readonly kind: 'exact'; readonly value: Rat }
  | { readonly kind: 'exhausted' }
  | { readonly kind: 'cancelled' };

/**
 * Source of tightening information about item values.
 *
 * Contract per item:
 * - `interval`: the new interval must be strictly contained in the previous one
 *   (monotonic tightening; use `exhausted` when no further tightening exists);
 * - `exact`: the certified value must lie inside the current interval; after an
 *   exact certificate the item is never queried again;
 * - `exhausted`: the provider cannot tighten further; the item keeps its interval;
 * - `cancelled`: the item is withdrawn and excluded from the ranking.
 */
export interface IntervalProvider<I> {
  refine(item: I): RefineResult;
}

/** Thrown when a provider breaks the monotonic-tightening / certificate contract. */
export class ProviderContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ProviderContractError';
  }
}

export interface TopKResult<I> {
  readonly k: number;
  /**
   * Determined prefix of the ascending order: a list of groups, earliest first.
   * Each group is either a singleton (position proven by disjoint intervals) or
   * a set of items proven equal by exact certificates.
   */
  readonly prefix: ReadonlyArray<ReadonlyArray<I>>;
  /**
   * Groups after the determined prefix, in interval order. Items inside a group
   * are mutually unseparated; the sorter never guesses their order (e.g. by
   * interval midpoints).
   */
  readonly pending: ReadonlyArray<ReadonlyArray<I>>;
  /** Items withdrawn by the provider. */
  readonly cancelled: ReadonlyArray<I>;
  /** Number of provider queries consumed. */
  readonly budgetUsed: number;
  /** True when the first k positions are fully determined (or nothing is pending). */
  readonly complete: boolean;
}

interface ItemState<I> {
  readonly item: I;
  interval: Interval;
  exact: boolean;
  exhausted: boolean;
  cancelled: boolean;
}

/**
 * Computes the top-k of items whose values are only observable through
 * successively tightening rational intervals supplied by an IntervalProvider.
 *
 * An ordering fact between two items is established only when their intervals
 * are strictly disjoint (a.hi < b.lo). Equality is established only when both
 * items hold exact certificates with the same value. Refinement budget is spent
 * only on items that can still influence the first k positions.
 */
export class TopKSorter<I> {
  private readonly provider: IntervalProvider<I>;
  private readonly budget: number;
  private budgetUsed = 0;
  private readonly states: Array<ItemState<I>> = [];

  constructor(provider: IntervalProvider<I>, options: { readonly budget: number }) {
    if (!Number.isInteger(options.budget) || options.budget < 0) {
      throw new RangeError('budget must be a non-negative integer');
    }
    this.provider = provider;
    this.budget = options.budget;
  }

  add(item: I, initial: Interval): void {
    if (initial.lo.gt(initial.hi)) {
      throw new ProviderContractError('initial interval has lo > hi');
    }
    this.states.push({
      item,
      interval: initial,
      exact: initial.lo.eq(initial.hi),
      exhausted: false,
      cancelled: false,
    });
  }

  run(k: number): TopKResult<I> {
    if (!Number.isInteger(k) || k < 0) {
      throw new RangeError('k must be a non-negative integer');
    }
    while (this.budgetUsed < this.budget) {
      const target = this.pickRefinementTarget(k);
      if (target === undefined) break;
      this.refineOne(target);
    }
    return this.buildResult(k);
  }

  /**
   * Widest refinable interval among unresolved components that can still
   * overlap the first k positions; undefined when the top-k is determined or
   * no relevant item can be refined further.
   */
  private pickRefinementTarget(k: number): ItemState<I> | undefined {
    let before = 0;
    let best: ItemState<I> | undefined;
    for (const component of this.components()) {
      if (before >= k) break;
      if (!isResolved(component)) {
        for (const state of component) {
          if (state.exact || state.exhausted) continue;
          if (best === undefined || widthOf(state).gt(widthOf(best))) {
            best = state;
          }
        }
      }
      before += component.length;
    }
    return best;
  }

  private refineOne(state: ItemState<I>): void {
    const result = this.provider.refine(state.item);
    this.budgetUsed += 1;
    switch (result.kind) {
      case 'interval': {
        const next = result.interval;
        if (next.lo.gt(next.hi)) {
          throw new ProviderContractError('provider returned an empty interval (lo > hi)');
        }
        if (next.lo.lt(state.interval.lo) || next.hi.gt(state.interval.hi)) {
          throw new ProviderContractError(
            `monotonic tightening violated: [${next.lo}, ${next.hi}] is not contained in [${state.interval.lo}, ${state.interval.hi}]`,
          );
        }
        if (next.lo.eq(state.interval.lo) && next.hi.eq(state.interval.hi)) {
          throw new ProviderContractError('tightening violated: interval did not shrink');
        }
        state.interval = next;
        if (next.lo.eq(next.hi)) state.exact = true;
        break;
      }
      case 'exact': {
        const value = result.value;
        if (value.lt(state.interval.lo) || value.gt(state.interval.hi)) {
          throw new ProviderContractError(
            `exact certificate ${value} lies outside the current interval [${state.interval.lo}, ${state.interval.hi}]`,
          );
        }
        state.interval = { lo: value, hi: value };
        state.exact = true;
        break;
      }
      case 'exhausted':
        state.exhausted = true;
        break;
      case 'cancelled':
        state.cancelled = true;
        break;
    }
  }

  /**
   * Connected components of the "not provably separated" graph over active
   * items, in ascending interval order. Two items are separated only by a
   * strict gap (a.hi < b.lo); touching intervals remain unseparated.
   */
  private components(): Array<Array<ItemState<I>>> {
    const active = this.states
      .filter((state) => !state.cancelled)
      .sort(
        (a, b) =>
          a.interval.lo.cmp(b.interval.lo) || a.interval.hi.cmp(b.interval.hi),
      );
    const result: Array<Array<ItemState<I>>> = [];
    let currentMaxHi: Rat | undefined;
    for (const state of active) {
      const last = result[result.length - 1];
      if (last !== undefined && currentMaxHi !== undefined && state.interval.lo.le(currentMaxHi)) {
        last.push(state);
        if (state.interval.hi.gt(currentMaxHi)) currentMaxHi = state.interval.hi;
      } else {
        result.push([state]);
        currentMaxHi = state.interval.hi;
      }
    }
    return result;
  }

  private buildResult(k: number): TopKResult<I> {
    const prefix: Array<Array<I>> = [];
    const pending: Array<Array<I>> = [];
    let prefixCount = 0;
    let inPrefix = true;
    for (const component of this.components()) {
      if (inPrefix && isResolved(component)) {
        prefix.push(component.map((state) => state.item));
        prefixCount += component.length;
      } else {
        inPrefix = false;
        pending.push(component.map((state) => state.item));
      }
    }
    return {
      k,
      prefix,
      pending,
      cancelled: this.states.filter((state) => state.cancelled).map((state) => state.item),
      budgetUsed: this.budgetUsed,
      complete: pending.length === 0 || prefixCount >= k,
    };
  }
}

/**
 * A component is resolved when it is a singleton (its position is proven by
 * disjoint intervals) or every member holds an equal exact certificate.
 */
function isResolved<I>(component: Array<ItemState<I>>): boolean {
  if (component.length === 1) return true;
  const first = component[0].interval.lo;
  return component.every((state) => state.exact && state.interval.lo.eq(first));
}

function widthOf<I>(state: ItemState<I>): Rat {
  return state.interval.hi.sub(state.interval.lo);
}
