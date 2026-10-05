export type PartitionId = string;
export type TxnId = string;

/**
 * Edge events carry a per-partition monotonic version assigned by the
 * partition's local lock manager. The detector applies them
 * last-write-wins per edge key, so delivery may be reordered.
 */
export type EdgeEvent =
  | { kind: "edge-upsert"; partition: PartitionId; from: TxnId; to: TxnId; version: number }
  | { kind: "edge-remove"; partition: PartitionId; from: TxnId; to: TxnId; version: number };

/**
 * Liveness events, also versioned per partition. Transaction ids must be
 * unique per incarnation (never reused after commit/abort).
 */
export type TxnEvent =
  | { kind: "txn-begin"; partition: PartitionId; txn: TxnId; version: number }
  | { kind: "txn-end"; partition: PartitionId; txn: TxnId; version: number };

export type DetectorEvent = EdgeEvent | TxnEvent;

export interface CycleEdge {
  partition: PartitionId;
  from: TxnId;
  to: TxnId;
  version: number;
}

/**
 * A detected cycle. `edges[i]` goes from `nodes[i]` to
 * `nodes[(i + 1) % length]`. `fingerprint` captures edge identities and
 * versions so a later abort can detect a stale snapshot.
 */
export interface CycleReport {
  length: number;
  nodes: TxnId[];
  edges: CycleEdge[];
  fingerprint: string;
}

export type ApplyResult =
  | { applied: true }
  | { applied: false; reason: "stale-version" | "self-loop-ignored" };

export type AbortResult =
  | { status: "aborted"; victim: TxnId }
  | { status: "stale-snapshot"; victim: TxnId; detail: string }
  | { status: "victim-not-live"; victim: TxnId }
  | { status: "victim-not-in-cycle"; victim: TxnId };
