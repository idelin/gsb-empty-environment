import {
  FailureReason,
  ObjectId,
  OpId,
  Operation,
  State,
  replay,
} from './operation';

export interface Receipt {
  readonly opId: OpId;
  readonly accepted: boolean;
}

export type OpStatus = 'pending' | 'confirmed' | 'rejected' | 'failed';

/**
 * Optimistic state store.
 *
 * Model: an initial snapshot plus the full op log in submission order.
 * Receipts may arrive out of order; each receipt either confirms an op
 * (moves it into the confirmed baseline) or rejects it (removes it from
 * the log entirely).
 *
 * The visible state is derived by a single ordered replay:
 *
 *   view = replay(initialSnapshot, nonRejectedOps in submission order)
 *
 * A single pass over the submission-ordered log is required: with
 * out-of-order receipts a confirmed op can sit behind a still-pending
 * predecessor it depends on, so splicing "baseline then pending" as two
 * separate replays would reorder interacting ops and silently drop the
 * confirmed one. Rejection therefore triggers a re-replay of the remaining
 * successors, and successors that reference an object which no longer
 * exists are marked 'dependency' failures and skipped. The store never
 * restores a stashed whole-state snapshot, so successful sibling ops
 * (confirmed or still-pending) survive a rejection untouched.
 */
export class OptimisticStore {
  private readonly initial: Map<ObjectId, number>;
  private readonly log: Operation[] = [];
  private readonly known = new Set<OpId>();
  private readonly confirmed = new Set<OpId>();
  private readonly rejected = new Set<OpId>();

  private dirty = true;
  private baselineDirty = true;
  private cachedBaseline: Map<ObjectId, number> = new Map();
  private cachedState: Map<ObjectId, number> = new Map();
  private cachedFailures: Map<OpId, FailureReason> = new Map();

  constructor(initial?: Iterable<readonly [ObjectId, number]>) {
    this.initial = new Map<ObjectId, number>(initial);
  }

  /** Appends an op to the pending log; it takes effect optimistically. */
  submit(op: Operation): void {
    if (this.known.has(op.opId)) {
      throw new Error(`duplicate opId: ${op.opId}`);
    }
    this.known.add(op.opId);
    this.log.push(op);
    this.dirty = true;
  }

  /**
   * Processes a server receipt. Receipts may arrive in any order; the
   * baseline is rebuilt from the confirmed set in submission order, so the
   * result is independent of arrival order.
   */
  handleReceipt(receipt: Receipt): void {
    const { opId, accepted } = receipt;
    if (!this.known.has(opId)) {
      throw new Error(`receipt for unknown op: ${opId}`);
    }
    if (this.confirmed.has(opId) || this.rejected.has(opId)) {
      throw new Error(`duplicate receipt for op: ${opId}`);
    }
    if (accepted) {
      this.confirmed.add(opId);
      this.baselineDirty = true;
    } else {
      this.rejected.add(opId);
    }
    this.dirty = true;
  }

  /** Current optimistic view: all non-rejected ops replayed in order. */
  getState(): State {
    this.recomputeIfNeeded();
    return this.cachedState;
  }

  /**
   * Confirmed-only state: the initial snapshot plus accepted ops, in
   * submission order. Note this can temporarily lag behind getState()
   * effects when a confirmed op's still-pending predecessors have no
   * receipt yet; it reflects only what the server has acknowledged.
   */
  getBaseline(): State {
    if (this.baselineDirty) {
      const confirmedOps = this.log.filter((op) => this.confirmed.has(op.opId));
      this.cachedBaseline = replay(this.initial, confirmedOps).state;
      this.baselineDirty = false;
    }
    return this.cachedBaseline;
  }

  /** opId -> reason for every op that did not take effect. */
  getFailures(): ReadonlyMap<OpId, FailureReason> {
    this.recomputeIfNeeded();
    return this.cachedFailures;
  }

  statusOf(opId: OpId): OpStatus {
    if (this.confirmed.has(opId)) return 'confirmed';
    if (this.rejected.has(opId)) return 'rejected';
    if (!this.known.has(opId)) throw new Error(`unknown op: ${opId}`);
    this.recomputeIfNeeded();
    return this.cachedFailures.has(opId) ? 'failed' : 'pending';
  }

  private recomputeIfNeeded(): void {
    if (!this.dirty) return;
    const surviving = this.log.filter((op) => !this.rejected.has(op.opId));
    const { state, failures } = replay(this.initial, surviving);
    for (const opId of this.rejected) {
      failures.set(opId, 'rejected');
    }
    this.cachedState = state;
    this.cachedFailures = failures;
    this.dirty = false;
  }
}
