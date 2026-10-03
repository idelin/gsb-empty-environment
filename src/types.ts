/**
 * Core ARIES data types: LSNs, log records, transaction table, dirty page table.
 *
 * Only "erasable" TypeScript syntax is used (no enums / namespaces / parameter
 * properties) so the sources run directly on Node >= 22.6 type stripping.
 */

export type LSN = number;
export type TxnId = number;
export type PageId = number;

/** LSN 0 is the null pointer of the log chains. */
export const NULL_LSN: LSN = 0;

export const LogType = {
  Update: 'UPDATE',
  Clr: 'CLR',
  Commit: 'COMMIT',
  Abort: 'ABORT',
  End: 'END',
  BeginCheckpoint: 'BEGIN_CHECKPOINT',
  EndCheckpoint: 'END_CHECKPOINT',
} as const;
export type LogType = (typeof LogType)[keyof typeof LogType];

export const TxnStatus = {
  Running: 'RUNNING',
  Committing: 'COMMITTING',
  Aborting: 'ABORTING',
} as const;
export type TxnStatus = (typeof TxnStatus)[keyof typeof TxnStatus];

interface BaseRecord {
  lsn: LSN;
  checksum: number;
}

export interface UpdateRecord extends BaseRecord {
  type: typeof LogType.Update;
  txnId: TxnId;
  prevLSN: LSN;
  pageId: PageId;
  beforeImage: number;
  afterImage: number;
}

/** Compensation log record written while undoing an UPDATE. */
export interface ClrRecord extends BaseRecord {
  type: typeof LogType.Clr;
  txnId: TxnId;
  prevLSN: LSN;
  /** Points at the next record of this txn still to undo (skipped-to during re-undo). */
  undoNextLSN: LSN;
  pageId: PageId;
  /** Value being undone (== afterImage of the compensated UPDATE). */
  beforeImage: number;
  /** Value restored by this CLR (== beforeImage of the compensated UPDATE). */
  afterImage: number;
}

export interface TxnRecord extends BaseRecord {
  type: typeof LogType.Commit | typeof LogType.Abort | typeof LogType.End;
  txnId: TxnId;
  prevLSN: LSN;
}

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface DirtyPageEntry {
  pageId: PageId;
  recLSN: LSN;
}

export interface BeginCheckpointRecord extends BaseRecord {
  type: typeof LogType.BeginCheckpoint;
}

export interface EndCheckpointRecord extends BaseRecord {
  type: typeof LogType.EndCheckpoint;
  txnTable: TxnTableEntry[];
  dirtyPageTable: DirtyPageEntry[];
}

export type LogRecord =
  | UpdateRecord
  | ClrRecord
  | TxnRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;

/** A log record as supplied by callers, before LSN assignment and checksumming. */
export type LogRecordPayload =
  | Omit<UpdateRecord, 'lsn' | 'checksum'>
  | Omit<ClrRecord, 'lsn' | 'checksum'>
  | Omit<TxnRecord, 'lsn' | 'checksum'>
  | Omit<BeginCheckpointRecord, 'lsn' | 'checksum'>
  | Omit<EndCheckpointRecord, 'lsn' | 'checksum'>;

export function cloneRecord(rec: LogRecord): LogRecord {
  if (rec.type === LogType.EndCheckpoint) {
    return {
      ...rec,
      txnTable: rec.txnTable.map((e) => ({ ...e })),
      dirtyPageTable: rec.dirtyPageTable.map((e) => ({ ...e })),
    };
  }
  return { ...rec };
}
