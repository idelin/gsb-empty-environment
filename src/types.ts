/**
 * Shared types for the ARIES write-ahead log and recovery engine.
 *
 * Only erasable TypeScript syntax is used so the sources run directly
 * under `node --experimental-strip-types` (Node >= 22.6).
 */

export type LSN = number;
export type TxnId = number;
export type PageId = string;

export const LogType = {
  BEGIN: "BEGIN",
  UPDATE: "UPDATE",
  COMMIT: "COMMIT",
  ABORT: "ABORT",
  END: "END",
  CLR: "CLR",
  CHECKPOINT: "CHECKPOINT",
} as const;
export type LogTypeName = (typeof LogType)[keyof typeof LogType];

export interface LogRecord {
  lsn: LSN;
  type: LogTypeName;
  txnId: TxnId; // -1 for CHECKPOINT
  /** LSN of the previous record of the same transaction (0 = none). */
  prevLSN: LSN;
  pageId?: PageId;
  beforeImage?: string;
  afterImage?: string;
  /** CLR only: LSN of the next record to undo (skips already-undone records). */
  undoNextLSN?: LSN;
}

export interface CheckpointRecord extends LogRecord {
  type: typeof LogType.CHECKPOINT;
  /** Transaction table snapshot: txnId -> entry. */
  att: Array<[TxnId, TransactionStatus, LSN]>;
  /** Dirty page table snapshot: pageId -> recLSN. */
  dpt: Array<[PageId, LSN]>;
}

export const TxnStatus = {
  RUNNING: "RUNNING",
  COMMITTED: "COMMITTED",
  ABORTING: "ABORTING",
} as const;
export type TransactionStatus = (typeof TxnStatus)[keyof typeof TxnStatus];

export interface TransactionTableEntry {
  txnId: TxnId;
  status: TransactionStatus;
  lastLSN: LSN;
}

export interface DirtyPageTableEntry {
  pageId: PageId;
  /** First LSN that may have dirtied the page since last flush. */
  recLSN: LSN;
}

export interface Page {
  pageId: PageId;
  pageLSN: LSN;
  data: string;
}

export interface AnalysisResult {
  transactionTable: Map<TxnId, TransactionTableEntry>;
  dirtyPageTable: Map<PageId, DirtyPageTableEntry>;
  /** LSN where the REDO scan must start (min recLSN). */
  redoStartLSN: LSN;
}
