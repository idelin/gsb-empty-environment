/**
 * A single rename/move intention in the distributed directory tree.
 *
 * Each operation carries a Lamport timestamp (`time`) and the issuing
 * `replica` id. The pair (time, replica) — extended with the payload for
 * total determinism — defines the deterministic total order used to
 * arbitrate conflicting concurrent operations.
 */
export interface MoveOp {
  /** Lamport logical timestamp assigned by the issuing replica. */
  readonly time: number;
  /** Unique id of the replica that issued the operation. */
  readonly replica: string;
  /** Id of the tree node being moved/renamed. */
  readonly node: string;
  /** Id of the destination parent directory. */
  readonly parent: string;
  /** Name of the node after the operation (rename support). */
  readonly name: string;
}

/** A node of the materialised directory tree. */
export interface TreeNode {
  readonly id: string;
  readonly name: string;
  /** Parent id, or null for the root. */
  readonly parent: string | null;
}

/** Initial (committed) shape of a node before any move operations. */
export interface BaseNode {
  readonly id: string;
  readonly name: string;
  readonly parent: string | null;
}

/**
 * Deterministic total order over operations:
 *   1. Lamport time (ascending) — causally later ops sort later;
 *   2. replica id — breaks ties between independent concurrent ops;
 *   3. payload fields — guarantees totality even for forged duplicates.
 */
export function compareOps(a: MoveOp, b: MoveOp): number {
  if (a.time !== b.time) return a.time - b.time;
  if (a.replica !== b.replica) return a.replica < b.replica ? -1 : 1;
  if (a.node !== b.node) return a.node < b.node ? -1 : 1;
  if (a.parent !== b.parent) return a.parent < b.parent ? -1 : 1;
  if (a.name !== b.name) return a.name < b.name ? -1 : 1;
  return 0;
}

/** Stable dedup key for an operation. */
export function opKey(op: MoveOp): string {
  return `${op.time}${op.replica}${op.node}${op.parent}${op.name}`;
}
