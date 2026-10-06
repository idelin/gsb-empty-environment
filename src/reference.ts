import {
  FailureReason,
  ObjectId,
  OpId,
  Operation,
  State,
  replay,
} from './operation';

export interface ReferenceResult {
  readonly state: Map<ObjectId, number>;
  readonly failures: Map<OpId, FailureReason>;
}

/**
 * Serial reference model: the semantics of the system as if every op were
 * applied exactly once, in submission order, on a single state, with
 * server-rejected ops removed beforehand.
 *
 * `verdicts` maps opId -> accepted flag; ops without a verdict are still
 * pending and count as optimistically applied. This is the ground truth the
 * OptimisticStore is checked against after every receipt, in any receipt
 * order.
 */
export function serialReference(
  initial: State,
  ops: readonly Operation[],
  verdicts: ReadonlyMap<OpId, boolean>,
): ReferenceResult {
  const failures = new Map<OpId, FailureReason>();
  const surviving = ops.filter((op) => {
    if (verdicts.get(op.opId) === false) {
      failures.set(op.opId, 'rejected');
      return false;
    }
    return true;
  });
  const { state, failures: replayFailures } = replay(initial, surviving);
  for (const [opId, reason] of replayFailures) {
    failures.set(opId, reason);
  }
  return { state, failures };
}
