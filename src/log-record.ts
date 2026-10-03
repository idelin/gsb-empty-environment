export type TxStatus = 'running' | 'committed' | 'aborted';

export interface TxTableEntry {
  txId: number;
  status: TxStatus;
  lastLSN: number;
  /** LSN of the transaction's begin record; lower bound for safe log pruning. */
  firstLSN: number;
}

export const NULL_LSN = 0;

interface BaseRecord {
  lsn: number;
  txId: number;
  prevLSN: number;
}

export interface BeginRecord extends BaseRecord {
  type: 'begin';
}

export interface UpdateRecord extends BaseRecord {
  type: 'update';
  pageId: number;
  before: number;
  after: number;
}

export interface CommitRecord extends BaseRecord {
  type: 'commit';
}

export interface AbortRecord extends BaseRecord {
  type: 'abort';
}

export interface EndRecord extends BaseRecord {
  type: 'end';
}

/** Compensation log record: logs the undo of an update. `after` is the restored value. */
export interface CLRRecord extends BaseRecord {
  type: 'clr';
  pageId: number;
  before: number;
  after: number;
  undoNextLSN: number;
}

export interface CheckpointRecord {
  lsn: number;
  type: 'checkpoint';
  dpt: Array<[pageId: number, recLSN: number]>;
  tt: Array<[txId: number, entry: TxTableEntry]>;
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | CLRRecord
  | CheckpointRecord;

export type TxLogRecord = Exclude<LogRecord, CheckpointRecord>;
