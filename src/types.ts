/** Log Sequence Number — monotonically increasing, starts at 1. */
export type LSN = number;
export type TxnId = number;
export type PageId = number;

export const LogType = {
  BEGIN: 'BEGIN',
  UPDATE: 'UPDATE',
  COMMIT: 'COMMIT',
  ABORT: 'ABORT',
  END: 'END',
  CLR: 'CLR',
  BEGIN_CHECKPOINT: 'BEGIN_CHECKPOINT',
  END_CHECKPOINT: 'END_CHECKPOINT',
} as const;
export type LogTypeName = (typeof LogType)[keyof typeof LogType];

export const TxnStatus = {
  RUNNING: 'RUNNING',
  COMMITTING: 'COMMITTING',
  ABORTING: 'ABORTING',
} as const;
export type TxnStatusName = (typeof TxnStatus)[keyof typeof TxnStatus];

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatusName;
  lastLSN: LSN;
}

/** txnId -> { status, lastLSN } */
export type TxnTable = Map<TxnId, TxnTableEntry>;
/** pageId -> recLSN (oldest LSN that may have dirtied the page) */
export type DirtyPageTable = Map<PageId, LSN>;

interface BaseRecord {
  lsn: LSN;
  prevLSN: LSN | null;
  txnId: TxnId | null;
}

export interface BeginRecord extends BaseRecord {
  type: typeof LogType.BEGIN;
  txnId: TxnId;
}
export interface UpdateRecord extends BaseRecord {
  type: typeof LogType.UPDATE;
  txnId: TxnId;
  pageId: PageId;
  before: string;
  after: string;
}
/** Compensation Log Record: describes an undo that has been performed. */
export interface ClrRecord extends BaseRecord {
  type: typeof LogType.CLR;
  txnId: TxnId;
  pageId: PageId;
  before: string;
  after: string;
  undoNextLSN: LSN | null;
}
export interface CommitRecord extends BaseRecord {
  type: typeof LogType.COMMIT;
  txnId: TxnId;
}
export interface AbortRecord extends BaseRecord {
  type: typeof LogType.ABORT;
  txnId: TxnId;
}
export interface EndRecord extends BaseRecord {
  type: typeof LogType.END;
  txnId: TxnId;
}
export interface BeginCheckpointRecord extends BaseRecord {
  type: typeof LogType.BEGIN_CHECKPOINT;
  txnId: null;
}
export interface DirtyPageEntry {
  pageId: PageId;
  recLSN: LSN;
}
export interface EndCheckpointRecord extends BaseRecord {
  type: typeof LogType.END_CHECKPOINT;
  txnId: null;
  txnTable: TxnTableEntry[];
  dirtyPages: DirtyPageEntry[];
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | ClrRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;

/** A record as supplied by callers; the WAL assigns the LSN. */
export type LogRecordInput =
  | Omit<BeginRecord, 'lsn'>
  | Omit<UpdateRecord, 'lsn'>
  | Omit<ClrRecord, 'lsn'>
  | Omit<CommitRecord, 'lsn'>
  | Omit<AbortRecord, 'lsn'>
  | Omit<EndRecord, 'lsn'>
  | Omit<BeginCheckpointRecord, 'lsn'>
  | Omit<EndCheckpointRecord, 'lsn'>;
