/** Log Sequence Number. */
export type LSN = number;

export const RecordType = {
  Update: 'UPDATE',
  Clr: 'CLR',
  Commit: 'COMMIT',
  Abort: 'ABORT',
  End: 'END',
  BeginCheckpoint: 'BEGIN_CHECKPOINT',
  EndCheckpoint: 'END_CHECKPOINT',
} as const;
export type RecordType = (typeof RecordType)[keyof typeof RecordType];

export const TxnStatus = {
  Running: 'RUNNING',
  Committed: 'COMMITTED',
  Aborting: 'ABORTING',
} as const;
export type TxnStatus = (typeof TxnStatus)[keyof typeof TxnStatus];

/** Stable error codes exported as metric labels. */
export const ErrorCode = {
  WalChecksumMismatch: 'WAL_CHECKSUM_MISMATCH',
  WalCorruptRecord: 'WAL_CORRUPT_RECORD',
  WalUnknownType: 'WAL_UNKNOWN_TYPE',
  PageNotFound: 'PAGE_NOT_FOUND',
  TxnNotFound: 'TXN_NOT_FOUND',
  InvalidOperation: 'INVALID_OPERATION',
} as const;
export type ErrorCode = (typeof ErrorCode)[keyof typeof ErrorCode];

export interface TxnTableEntry {
  txnId: number;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface DirtyPageEntry {
  pageId: string;
  recLSN: LSN;
}

interface BaseRecord {
  lsn: LSN;
  txnId: number;
  prevLSN: LSN;
}

export interface UpdateRecord extends BaseRecord {
  type: typeof RecordType.Update;
  pageId: string;
  before: unknown;
  after: unknown;
}

/** Compensation Log Record written by Undo. `after` is the value restored. */
export interface ClrRecord extends BaseRecord {
  type: typeof RecordType.Clr;
  pageId: string;
  undoNextLSN: LSN;
  after: unknown;
}

export interface CommitRecord extends BaseRecord {
  type: typeof RecordType.Commit;
}

export interface AbortRecord extends BaseRecord {
  type: typeof RecordType.Abort;
}

export interface EndRecord extends BaseRecord {
  type: typeof RecordType.End;
}

export interface BeginCheckpointRecord {
  type: typeof RecordType.BeginCheckpoint;
  lsn: LSN;
}

export interface EndCheckpointRecord {
  type: typeof RecordType.EndCheckpoint;
  lsn: LSN;
  beginLSN: LSN;
  txnTable: TxnTableEntry[];
  dirtyPages: DirtyPageEntry[];
}

export type LogRecord =
  | UpdateRecord
  | ClrRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;

/** Any log record without its LSN assigned yet (WalWriter assigns it). */
export type LogRecordPayload =
  | Omit<UpdateRecord, 'lsn'>
  | Omit<ClrRecord, 'lsn'>
  | Omit<CommitRecord, 'lsn'>
  | Omit<AbortRecord, 'lsn'>
  | Omit<EndRecord, 'lsn'>
  | Omit<BeginCheckpointRecord, 'lsn'>
  | Omit<EndCheckpointRecord, 'lsn'>;
