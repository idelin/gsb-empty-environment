/**
 * OptimisticStore — an optimistic state container.
 *
 * The store keeps two layers:
 *
 *   - `baseline`: the server-confirmed state. It only ever advances by
 *     committing acknowledged operations, in submission order.
 *   - `pending`: operations submitted locally but not yet resolved by a
 *     server receipt. The visible state is `baseline` with every
 *     currently-applicable pending op replayed on top, in submission order.
 *
 * Receipts may arrive out of order:
 *
 *   - An accept is buffered until every earlier op is resolved, then the op
 *     is committed into the baseline (head-of-line drain).
 *   - A reject takes effect immediately: the op is removed from the pending
 *     queue and its successors are replayed. Successors that reference an
 *     object which no longer exists are marked `dependency-failed`.
 *
 * `dependency-failed` is a *derived, recoverable* marking, recomputed by
 * replaying the queue after every change: if the op that destroyed the
 * object is itself rejected later, the dependent op becomes applicable
 * again and replays normally. An op only leaves the queue when its own
 * receipt arrives, so a late server accept for a previously-failed op is
 * still honored.
 *
 * The store never restores a saved snapshot: a rejection surgically
 * removes exactly one op, so already-committed baseline entries and
 * unrelated optimistic ops can never be wiped out.
 */

export type OpKind = 'create' | 'update' | 'delete';

export interface Operation<V = unknown> {
  /** Unique operation id, assigned by the caller. */
  readonly id: string;
  readonly kind: OpKind;
  /** Id of the object this operation touches. */
  readonly target: string;
  /** `create`: full value. `update`: merged over the current value. */
  readonly value?: V;
}

export type TerminalStatus = 'committed' | 'rejected';
export type OpStatus = 'pending' | 'dependency-failed' | TerminalStatus;

export interface PendingEntry<V = unknown> {
  readonly op: Operation<V>;
  /** True while the op cannot be replayed over the current baseline. */
  readonly failed: boolean;
}

export interface OpResolution<V = unknown> {
  readonly op: Operation<V>;
  readonly status: TerminalStatus;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function mergeValue<V>(prev: V, next: V): V {
  if (isPlainObject(prev) && isPlainObject(next)) {
    return { ...prev, ...next } as V;
  }
  return next;
}

/** Whether `op` can be applied to `state`. */
export function applies<V>(state: ReadonlyMap<string, V>, op: Operation<V>): boolean {
  switch (op.kind) {
    case 'create':
      return !state.has(op.target);
    case 'update':
    case 'delete':
      return state.has(op.target);
  }
}

/** Apply `op` to `state` in place. Caller must ensure `applies(state, op)`. */
export function applyOp<V>(state: Map<string, V>, op: Operation<V>): void {
  switch (op.kind) {
    case 'create':
      state.set(op.target, op.value as V);
      break;
    case 'update':
      state.set(op.target, mergeValue(state.get(op.target) as V, op.value as V));
      break;
    case 'delete':
      state.delete(op.target);
      break;
  }
}

export class OptimisticStore<V = unknown> {
  readonly #baseline = new Map<string, V>();
  #pending: PendingEntry<V>[] = [];
  /** Buffered accept receipts for ops that are still pending. */
  readonly #accepts = new Set<string>();
  /** Terminal resolutions in the order they happened. */
  readonly #resolutions: OpResolution<V>[] = [];
  /** Every op id ever submitted, for duplicate/unknown detection. */
  readonly #known = new Set<string>();

  constructor(initial?: Iterable<readonly [string, V]>) {
    if (initial) {
      for (const [key, value] of initial) this.#baseline.set(key, value);
    }
  }

  /** Server-confirmed state. */
  get baseline(): ReadonlyMap<string, V> {
    return this.#baseline;
  }

  /** Unresolved ops with their derived failure flags, in submission order. */
  get pending(): readonly PendingEntry<V>[] {
    return this.#pending;
  }

  /** Terminal resolutions (committed / rejected). */
  get resolutions(): readonly OpResolution<V>[] {
    return this.#resolutions;
  }

  statusOf(opId: string): OpStatus | undefined {
    if (!this.#known.has(opId)) return undefined;
    const entry = this.#pending.find((e) => e.op.id === opId);
    if (entry) return entry.failed ? 'dependency-failed' : 'pending';
    return this.#resolutions.find((r) => r.op.id === opId)?.status;
  }

  /** Visible state: baseline with all applicable pending ops replayed. */
  state(): Map<string, V> {
    const view = new Map(this.#baseline);
    for (const entry of this.#pending) {
      if (!entry.failed) applyOp(view, entry.op);
    }
    return view;
  }

  submit(op: Operation<V>): void {
    if (this.#known.has(op.id)) {
      throw new Error(`duplicate operation id: ${op.id}`);
    }
    if (op.kind !== 'delete' && op.value === undefined) {
      throw new Error(`operation ${op.id} of kind ${op.kind} requires a value`);
    }
    this.#known.add(op.id);
    this.#pending.push({ op, failed: false });
    this.#reconcile();
  }

  /**
   * Process a server receipt. Returns false (no-op) for unknown, duplicate,
   * or already-resolved op ids.
   */
  receiveReceipt(opId: string, accepted: boolean): boolean {
    if (!this.#known.has(opId)) return false;
    const index = this.#pending.findIndex((e) => e.op.id === opId);
    if (index === -1) return false; // already resolved

    if (accepted) {
      // Buffer until every earlier op is resolved, then commit in order.
      // Honored even for ops currently marked dependency-failed: the cause
      // of the failure may itself be rejected before this op commits.
      this.#accepts.add(opId);
    } else {
      // Surgical removal: drop only this op, replay its successors.
      const [entry] = this.#pending.splice(index, 1);
      this.#accepts.delete(opId);
      this.#resolutions.push({ op: entry.op, status: 'rejected' });
    }
    this.#drain();
    this.#reconcile();
    return true;
  }

  /** Commit accepted ops from the head of the queue, in submission order. */
  #drain(): void {
    while (this.#pending.length > 0) {
      const head = this.#pending[0];
      if (!this.#accepts.has(head.op.id)) break;
      this.#accepts.delete(head.op.id);
      this.#pending.shift();
      // Invariant: an accepted head always applies to the baseline, because
      // the baseline is exactly the confirmed prefix of the submission
      // order — the same state the server applied the op to.
      applyOp(this.#baseline, head.op);
      this.#resolutions.push({ op: head.op, status: 'committed' });
    }
  }

  /**
   * Replay the queue over the baseline and recompute failure flags. An op
   * whose target no longer exists (or already exists, for `create`) is
   * flagged `dependency-failed` and skipped by the visible state; the flag
   * clears automatically if a later change makes the op applicable again.
   */
  #reconcile(): void {
    const view = new Map(this.#baseline);
    for (const entry of this.#pending) {
      if (applies(view, entry.op)) {
        applyOp(view, entry.op);
        entry.failed = false;
      } else {
        entry.failed = true;
      }
    }
  }
}
