/**
 * ARIES WAL log record definitions.
 *
 * LSNs are assigned by a single-node deterministic logical clock
 * (a strictly monotonic counter owned by the WAL manager).
 */

export type LSN = number;

/** LSN meaning "no previous record" in a transaction's prevLSN chain. */
export const NULL_LSN: LSN = 0;

export type PageID = string;
export type TxnID = number;

export const LogRecordType = {
  UPDATE: 'UPDATE',
  COMMIT: 'COMMIT',
  ABORT: 'ABORT',
  END: 'END',
  /** Compensation log record: redo-only record written while undoing. */
  CLR: 'CLR',
  /** Fuzzy checkpoint: carries a snapshot of the TxnTable + DirtyPageTable. */
  CHECKPOINT: 'CHECKPOINT',
} as const;
export type LogRecordType = (typeof LogRecordType)[keyof typeof LogRecordType];

export interface TxnTableEntrySnapshot {
  txnId: TxnID;
  status: 'RUNNING' | 'COMMITTED' | 'ABORTED';
  lastLSN: LSN;
}

export interface DirtyPageTableSnapshot {
  [pageId: string]: LSN; // pageID -> recLSN
}

interface BaseRecord {
  lsn: LSN;
  prevLSN: LSN;
  txnId: TxnID;
}

export interface UpdateRecord extends BaseRecord {
  type: typeof LogRecordType.UPDATE;
  pageId: PageID;
  key: string;
  before: unknown;
  after: unknown;
}

export interface CommitRecord extends BaseRecord {
  type: typeof LogRecordType.COMMIT;
}

export interface AbortRecord extends BaseRecord {
  type: typeof LogRecordType.ABORT;
}

export interface EndRecord extends BaseRecord {
  type: typeof LogRecordType.END;
}

export interface ClrRecord extends BaseRecord {
  type: typeof LogRecordType.CLR;
  pageId: PageID;
  key: string;
  /** Redo image: the before-image of the UPDATE being compensated. */
  after: unknown;
  /** LSN of the UPDATE record this CLR compensates. */
  undoneLSN: LSN;
  /** Where undo of this transaction resumes (prevLSN of the undone record). */
  undoNextLSN: LSN;
}

export interface CheckpointRecord {
  type: typeof LogRecordType.CHECKPOINT;
  lsn: LSN;
  txnTable: TxnTableEntrySnapshot[];
  dirtyPageTable: DirtyPageTableSnapshot;
}

export type LogRecord =
  | UpdateRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | ClrRecord
  | CheckpointRecord;

/** Records that carry a transaction id and participate in prevLSN chains. */
export type TxnLogRecord = Exclude<LogRecord, CheckpointRecord>;

export function isPageModifying(r: LogRecord): r is UpdateRecord | ClrRecord {
  return r.type === LogRecordType.UPDATE || r.type === LogRecordType.CLR;
}

export function serializeRecord(r: LogRecord): string {
  return JSON.stringify(r);
}

export function deserializeRecord(line: string): LogRecord {
  const parsed = JSON.parse(line) as LogRecord;
  if (typeof parsed.lsn !== 'number' || typeof parsed.type !== 'string') {
    throw new Error(`malformed WAL record: ${line}`);
  }
  return parsed;
}
