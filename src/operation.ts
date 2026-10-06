export type ObjectId = string;
export type OpId = string;

/** State is a map of object id -> value. */
export type State = ReadonlyMap<ObjectId, number>;

export interface CreateOp {
  readonly opId: OpId;
  readonly kind: 'create';
  readonly target: ObjectId;
  readonly value: number;
}

export interface UpdateOp {
  readonly opId: OpId;
  readonly kind: 'update';
  readonly target: ObjectId;
  readonly value: number;
}

export interface DeleteOp {
  readonly opId: OpId;
  readonly kind: 'delete';
  readonly target: ObjectId;
}

export type Operation = CreateOp | UpdateOp | DeleteOp;

/**
 * Why an operation did not take effect:
 * - 'rejected':   the server explicitly refused it.
 * - 'dependency': it references an object that does not exist when its turn
 *                 comes (e.g. the create/delete it relied on was rejected).
 * - 'conflict':   a create whose object already exists.
 */
export type FailureReason = 'rejected' | 'dependency' | 'conflict';

export type ApplyResult = 'applied' | 'dependency' | 'conflict';

/**
 * Applies a single op to a mutable state map. Inapplicable ops are skipped
 * (state untouched) and reported, never thrown.
 */
export function applyOp(state: Map<ObjectId, number>, op: Operation): ApplyResult {
  switch (op.kind) {
    case 'create':
      if (state.has(op.target)) return 'conflict';
      state.set(op.target, op.value);
      return 'applied';
    case 'update':
      if (!state.has(op.target)) return 'dependency';
      state.set(op.target, op.value);
      return 'applied';
    case 'delete':
      if (!state.has(op.target)) return 'dependency';
      state.delete(op.target);
      return 'applied';
  }
}

export interface ReplayResult {
  readonly state: Map<ObjectId, number>;
  readonly failures: Map<OpId, FailureReason>;
}

/**
 * Replays ops in order on top of `base`. Because inapplicable ops are pure
 * skips, replay composes: replay(replay(s, A), B) === replay(s, A ++ B).
 * This is what lets the store rebuild its confirmed baseline and then its
 * optimistic view as two ordered replays instead of stashing snapshots.
 */
export function replay(base: State, ops: readonly Operation[]): ReplayResult {
  const state = new Map<ObjectId, number>(base);
  const failures = new Map<OpId, FailureReason>();
  for (const op of ops) {
    const result = applyOp(state, op);
    if (result !== 'applied') failures.set(op.opId, result);
  }
  return { state, failures };
}
