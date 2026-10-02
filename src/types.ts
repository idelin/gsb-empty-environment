/**
 * Shared types for the acyclic tree CRDT.
 *
 * Every operation carries a hybrid logical timestamp:
 *  - `lamport`  : Lamport clock value, gives causal ("happened-before") order.
 *  - `replica`  : unique replica id, deterministic tie-breaker.
 *  - `seq`      : per-replica sequence number, makes the op id unique.
 *
 * The triple (lamport, replica, seq) forms a deterministic total order over
 * all operations, which is what every replica uses to arbitrate conflicts
 * (including cycle breaking) identically.
 */

export type NodeId = string;
export type ReplicaId = string;

export interface Timestamp {
  lamport: number;
  replica: ReplicaId;
  seq: number;
}

export const ROOT_ID: NodeId = "root";

export interface CreateOp {
  kind: "create";
  id: string;
  ts: Timestamp;
  node: NodeId;
  name: string;
  parent: NodeId;
}

export interface MoveOp {
  kind: "move";
  id: string;
  ts: Timestamp;
  node: NodeId;
  /** null means "detach to root". */
  newParent: NodeId;
}

export interface RenameOp {
  kind: "rename";
  id: string;
  ts: Timestamp;
  node: NodeId;
  name: string;
}

export type Op = CreateOp | MoveOp | RenameOp;

/** Deterministic total order over timestamps. */
export function compareTs(a: Timestamp, b: Timestamp): number {
  if (a.lamport !== b.lamport) return a.lamport - b.lamport;
  if (a.replica !== b.replica) return a.replica < b.replica ? -1 : 1;
  return a.seq - b.seq;
}

/** Deterministic total order over operations (used as the merge/reduction order). */
export function compareOps(a: Op, b: Op): number {
  const byTs = compareTs(a.ts, b.ts);
  if (byTs !== 0) return byTs;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export interface NodeState {
  id: NodeId;
  name: string;
  parent: NodeId | null;
  children: NodeId[];
}

export interface DroppedOp {
  op: Op;
  reason: string;
}
