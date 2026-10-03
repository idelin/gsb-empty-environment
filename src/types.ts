export type LSN = number;
export type TxnId = number;
export type PageId = number;

export type LogRecordType =
  | 'BEGIN'
  | 'UPDATE'
  | 'COMMIT'
  | 'ABORT'
  | 'END'
  | 'CLR'
  | 'CHECKPOINT';

export type TxnStatus = 'RUNNING' | 'COMMITTED' | 'ABORTING';

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

/**
 * ARIES log record.
 * `before`/`after` hold key images; `null` means "key absent" (null is a
 * reserved sentinel, not a storable value).
 */
export interface LogRecord {
  lsn: LSN;
  type: LogRecordType;
  txnId: TxnId; // 0 for CHECKPOINT
  prevLSN: LSN; // 0 = null (start of txn chain)
  pageId?: PageId;
  key?: string;
  before?: unknown;
  after?: unknown;
  undoNextLSN?: LSN; // CLR only
  tt?: Record<string, TxnTableEntry>; // CHECKPOINT: transaction table snapshot
  dpt?: Record<string, LSN>; // CHECKPOINT: dirty page table snapshot
}

export interface Page {
  pageId: PageId;
  pageLSN: LSN;
  data: Record<string, unknown>;
}
