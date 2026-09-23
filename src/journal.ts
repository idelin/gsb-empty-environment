/**
 * Replayable op journal with snapshot isolation.
 *
 * All state changes are expressed as serializable ops applied by a pure
 * reducer. `transact` applies a batch atomically: the committed snapshot is
 * swapped in one step and deep-frozen, so readers can never observe a
 * transaction's intermediate state and can never mutate history.
 * `replay()` rebuilds the state purely from the op log.
 */
export interface Op {
  type: string;
  [key: string]: unknown;
}

export type Reducer<S> = (state: S, op: Op) => S;
export type Listener<S> = (snapshot: S, op: Op) => void;

export function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const key of Object.keys(value as Record<string, unknown>)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
  }
  return value;
}

export class Store<S> {
  #reduce: Reducer<S>;
  #initial: S;
  #committed: S;
  #log: Op[] = [];
  #listeners = new Set<Listener<S>>();

  constructor(reduce: Reducer<S>, initial: S) {
    this.#reduce = reduce;
    this.#initial = initial;
    this.#committed = deepFreeze(initial);
  }

  get log(): readonly Op[] {
    return this.#log;
  }

  /**
   * Atomically commit a batch of ops. Listeners fire only after the commit
   * and every listener receives the final committed snapshot — intermediate
   * states of the batch are never observable.
   */
  transact(ops: Op[]): S {
    let next = this.#committed;
    for (const op of ops) next = this.#reduce(next, op);
    deepFreeze(next);
    this.#log.push(...ops);
    this.#committed = next;
    for (const op of ops) {
      for (const listener of [...this.#listeners]) listener(next, op);
    }
    return next;
  }

  /** The committed snapshot. Never reflects in-flight transactions. */
  snapshot(): S {
    return this.#committed;
  }

  /** Rebuild state purely from the op log; must equal `snapshot()`. */
  replay(): S {
    let state = this.#initial;
    for (const op of this.#log) state = this.#reduce(state, op);
    return state;
  }

  /** Subscribe to committed ops. Returned unsubscribe is idempotent. */
  subscribe(listener: Listener<S>): () => void {
    this.#listeners.add(listener);
    let active = true;
    return () => {
      if (!active) return;
      active = false;
      this.#listeners.delete(listener);
    };
  }
}
