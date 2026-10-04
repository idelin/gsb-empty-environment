/**
 * ARIES WAL log record definitions.
 *
 * LSNs are monotonically increasing integers; NULL_LSN (0) terminates the
 * per-transaction prevLSN / undoNextLSN chains.
 */

export type LSN = number;
export const NULL_LSN: LSN = 0;

export type TxnId = number;
export type PageId = string;

/** Logical value stored in a page (kept JSON-serializable for persistence). */
export type PageValue = number | string | boolean | null;

export const LogRecordType = {
  BEGIN: "BEGIN",
  UPDATE: "UPDATE",
  COMMIT: "COMMIT",
  ABORT: "ABORT",
  END: "END",
  CLR: "CLR",
  CHECKPOINT: "CHECKPOINT",
} as const;
export type LogRecordType = (typeof LogRecordType)[keyof typeof LogRecordType];

interface TxnRecordBase {
  lsn: LSN;
  txnId: TxnId;
  /** LSN of the previous record of the same transaction (NULL_LSN if none). */
  prevLSN: LSN;
}

export interface BeginRecord extends TxnRecordBase {
  type: typeof LogRecordType.BEGIN;
}

export interface UpdateRecord extends TxnRecordBase {
  type: typeof LogRecordType.UPDATE;
  pageId: PageId;
  before: PageValue;
  after: PageValue;
}

/**
 * Compensation Log Record: logs the undo of an UPDATE. `undoNextLSN` is the
 * LSN of the next record to undo for this transaction; it lets restart-undo
 * skip work already compensated before a crash (no double undo).
 */
export interface ClrRecord extends TxnRecordBase {
  type: typeof LogRecordType.CLR;
  pageId: PageId;
  before: PageValue;
  after: PageValue;
  undoNextLSN: LSN;
}

export interface CommitRecord extends TxnRecordBase {
  type: typeof LogRecordType.COMMIT;
}

export interface AbortRecord extends TxnRecordBase {
  type: typeof LogRecordType.ABORT;
}

export interface EndRecord extends TxnRecordBase {
  type: typeof LogRecordType.END;
}

export interface TxnTableEntrySnapshot {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface DirtyPageSnapshot {
  pageId: PageId;
  recLSN: LSN;
}

export interface CheckpointRecord {
  type: typeof LogRecordType.CHECKPOINT;
  lsn: LSN;
  txnId: -1;
  prevLSN: typeof NULL_LSN;
  txnTable: TxnTableEntrySnapshot[];
  dirtyPageTable: DirtyPageSnapshot[];
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | ClrRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | CheckpointRecord;

export const TxnStatus = {
  RUNNING: "RUNNING",
  COMMITTED: "COMMITTED",
  ABORTING: "ABORTING",
} as const;
export type TxnStatus = (typeof TxnStatus)[keyof typeof TxnStatus];

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export function isRedoable(record: LogRecord): record is UpdateRecord | ClrRecord {
  return record.type === LogRecordType.UPDATE || record.type === LogRecordType.CLR;
}

export function serializeRecord(record: LogRecord): string {
  return JSON.stringify(record);
}

export function deserializeRecord(line: string): LogRecord {
  return JSON.parse(line) as LogRecord;
}
