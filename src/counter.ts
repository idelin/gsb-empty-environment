/**
 * Observed-reset multi-replica counter (state-based CRDT).
 *
 * Semantics:
 * - Every mutation is an operation with a globally unique id
 *   (`replicaId#logicalSeq`; a per-replica logical sequence, never wall-clock
 *   time, so no clock skew / ordering assumptions are involved).
 * - `increment` contributes `amount` to the counter.
 * - `reset` names the exact set of operation ids it had observed when issued
 *   and clears exactly those contributions. Increments it did not observe
 *   (concurrent, or delayed in transit) survive the reset.
 * - Merge is set union over operations, deduplicated by unique op id.
 *   It is commutative, associative and idempotent, so message order,
 *   duplication and re-merge do not affect the converged state.
 */

export type OpId = string;

export interface IncOp {
  readonly kind: "inc";
  readonly id: OpId;
  readonly amount: number;
}

export interface ResetOp {
  readonly kind: "reset";
  readonly id: OpId;
  /** Ids of every operation this reset had observed when it was issued. */
  readonly observed: readonly OpId[];
}

export type CounterOp = IncOp | ResetOp;

/** JSON-serializable, deterministically ordered replica state. */
export interface CounterSnapshot {
  readonly ops: readonly CounterOp[];
}

export class ObservedResetCounter {
  private readonly replicaId: string;
  /** Per-replica logical sequence used only to mint unique op ids. */
  private seq = 0;
  /** Applied operations, deduplicated by unique op id. */
  private readonly ops = new Map<OpId, CounterOp>();

  constructor(replicaId: string, snapshot?: CounterSnapshot) {
    if (!replicaId) throw new Error("replicaId must be a non-empty string");
    this.replicaId = replicaId;
    if (snapshot) this.merge(snapshot);
  }

  /** Number of distinct operations known to this replica. */
  get size(): number {
    return this.ops.size;
  }

  has(id: OpId): boolean {
    return this.ops.has(id);
  }

  /** Local increment; returns the op message to broadcast to peers. */
  increment(amount = 1): IncOp {
    if (!Number.isSafeInteger(amount) || amount <= 0) {
      throw new RangeError("increment amount must be a positive safe integer");
    }
    const op: IncOp = { kind: "inc", id: this.nextId(), amount };
    this.apply(op);
    return op;
  }

  /**
   * Local reset; captures the ids of all operations observed so far and
   * returns the op message to broadcast. Only observed contributions are
   * cleared; concurrently created increments survive.
   */
  reset(): ResetOp {
    const op: ResetOp = {
      kind: "reset",
      id: this.nextId(),
      observed: [...this.ops.keys()],
    };
    this.apply(op);
    return op;
  }

  /**
   * Apply one op message. Idempotent: an op whose id is already known is
   * ignored, so duplicated deliveries are harmless.
   *
   * @returns `true` if the op was new, `false` if it was a duplicate.
   */
  apply(op: CounterOp): boolean {
    validateOp(op);
    if (this.ops.has(op.id)) return false;
    this.ops.set(op.id, cloneOp(op));
    return true;
  }

  /**
   * State-based merge: union of operation sets, deduplicated by op id.
   * Accepts snapshots or other replicas; safe to call repeatedly with the
   * same source.
   */
  merge(...sources: ReadonlyArray<CounterSnapshot | ObservedResetCounter>): this {
    for (const source of sources) {
      const ops =
        source instanceof ObservedResetCounter ? source.snapshot().ops : source.ops;
      for (const op of ops) this.apply(op);
    }
    return this;
  }

  /** Deterministic (sorted by op id), deep-copied, JSON-safe state export. */
  snapshot(): CounterSnapshot {
    const ops = [...this.ops.values()].sort((a, b) => compareIds(a.id, b.id));
    return { ops: ops.map(cloneOp) };
  }

  /**
   * Current value: sum of all increments not named by any known reset's
   * observed set.
   */
  value(): number {
    const cleared = new Set<OpId>();
    for (const op of this.ops.values()) {
      if (op.kind === "reset") {
        for (const id of op.observed) cleared.add(id);
      }
    }
    let total = 0;
    for (const op of this.ops.values()) {
      if (op.kind === "inc" && !cleared.has(op.id)) total += op.amount;
    }
    return total;
  }

  /** Structural equality of converged state (order-independent). */
  equals(other: ObservedResetCounter): boolean {
    return snapshotsEqual(this.snapshot(), other.snapshot());
  }

  private nextId(): OpId {
    this.seq += 1;
    return `${this.replicaId}#${this.seq}`;
  }
}

function compareIds(a: OpId, b: OpId): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function cloneOp(op: CounterOp): CounterOp {
  return op.kind === "inc"
    ? { kind: "inc", id: op.id, amount: op.amount }
    : { kind: "reset", id: op.id, observed: [...op.observed] };
}

function validateOp(op: CounterOp): void {
  if (!op || typeof op.id !== "string" || op.id.length === 0) {
    throw new TypeError("operation must carry a non-empty unique id");
  }
  if (op.kind === "inc") {
    if (!Number.isSafeInteger(op.amount) || op.amount <= 0) {
      throw new TypeError("inc op amount must be a positive safe integer");
    }
  } else if (op.kind === "reset") {
    if (!Array.isArray(op.observed) || op.observed.some((id) => typeof id !== "string")) {
      throw new TypeError("reset op observed must be an array of op ids");
    }
  } else {
    throw new TypeError(`unknown op kind: ${String((op as { kind: unknown }).kind)}`);
  }
}

export function snapshotsEqual(a: CounterSnapshot, b: CounterSnapshot): boolean {
  const sortOps = (s: CounterSnapshot): CounterOp[] =>
    [...s.ops].sort((x, y) => compareIds(x.id, y.id));
  const left = sortOps(a);
  const right = sortOps(b);
  if (left.length !== right.length) return false;
  return left.every((op, i) => opsEqual(op, right[i]));
}

function opsEqual(a: CounterOp, b: CounterOp): boolean {
  if (a.id !== b.id || a.kind !== b.kind) return false;
  if (a.kind === "inc" && b.kind === "inc") return a.amount === b.amount;
  if (a.kind === "reset" && b.kind === "reset") {
    return (
      a.observed.length === b.observed.length &&
      [...a.observed].sort().every((id, i) => id === [...b.observed].sort()[i])
    );
  }
  return false;
}
