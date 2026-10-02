/** 事件与审计记录的公共类型定义。 */

export type TxnId = string;

/** 两阶段第一阶段：预备事件，声明事务的读写集合。 */
export interface PrepareEvent {
  kind: 'prepare';
  txnId: TxnId;
  /** 逻辑时钟，用于确定性排序（与到达顺序无关）。 */
  lamport: number;
  reads: string[];
  writes: Record<string, string>;
}

export interface CommitEvent {
  kind: 'commit';
  txnId: TxnId;
}

export interface AbortEvent {
  kind: 'abort';
  txnId: TxnId;
}

export type TxnEvent = PrepareEvent | CommitEvent | AbortEvent;

/** 本地确定性日志条目：seq 为单调递增的日志位置。 */
export interface LogEntry {
  seq: number;
  event: TxnEvent;
}

/** 确定性排序键：(lamport, txnId) 字典序。 */
export type OrderKey = readonly [number, string];

export function orderKeyOf(e: PrepareEvent): OrderKey {
  return [e.lamport, e.txnId] as const;
}

export function compareOrderKey(a: OrderKey, b: OrderKey): number {
  if (a[0] !== b[0]) return a[0] - b[0];
  return a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0;
}

/** 未决冲突的决胜证据：可审计，内容只取决于事件本身，与到达顺序无关。 */
export interface TieBreakEvidence {
  type: 'tie-break';
  txnA: TxnId;
  txnB: TxnId;
  conflictKeys: string[];
  orderKeyA: OrderKey;
  orderKeyB: OrderKey;
  winner: TxnId;
  rule: string;
}

export interface ApplyRecord {
  type: 'apply';
  txnId: TxnId;
  orderKey: OrderKey;
  writes: Record<string, string>;
  partitions: number[];
}

export interface AbortRecord {
  type: 'abort-applied';
  txnId: TxnId;
  orderKey: OrderKey;
}

export interface RejectPrepareEvidence {
  type: 'reject-prepare';
  txnId: TxnId;
  reason: 'max-pending-exceeded' | 'duplicate-prepare';
  pendingCount: number;
  maxPending: number;
}

export interface OrphanEventRecord {
  type: 'orphan-event';
  txnId: TxnId;
  eventKind: 'commit' | 'abort';
  reason: 'unknown-txn';
}

export interface DuplicateEventRecord {
  type: 'duplicate-event';
  txnId: TxnId;
  eventKind: TxnEvent['kind'];
}

export type AuditRecord =
  | TieBreakEvidence
  | ApplyRecord
  | AbortRecord
  | RejectPrepareEvidence
  | OrphanEventRecord
  | DuplicateEventRecord;

export const TIE_BREAK_RULE =
  'order by (lamport, txnId) lexicographic; smaller order key is applied first';
