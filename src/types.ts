export type TxnId = string;
export type PartitionId = string;

/** A versioned wait-for edge: transaction `from` is waiting on transaction `to`. */
export interface WaitEdge {
  from: TxnId;
  to: TxnId;
  /** Per-edge monotonic version assigned by the reporting partition. */
  version: number;
  /** Partition that reported this edge. */
  partition: PartitionId;
}

/** Messages streamed from partitions to the global coordinator. */
export type EdgeMessage =
  | { kind: 'add'; from: TxnId; to: TxnId; version: number; partition: PartitionId }
  | { kind: 'remove'; from: TxnId; to: TxnId; version: number; partition: PartitionId };

/** A directed cycle in the global wait-for graph. */
export interface Cycle {
  /** Nodes in cycle order; the cycle closes back to nodes[0]. */
  nodes: TxnId[];
  /** Edges (with versions) along the cycle; edges[i] goes nodes[i] -> nodes[(i+1) % n]. */
  edges: WaitEdge[];
}

export function edgeKey(from: TxnId, to: TxnId): string {
  return from + '' + to;
}
