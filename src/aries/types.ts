export type TxnStatus = 'RUNNING' | 'COMMITTED' | 'ABORTING';

export interface TxnTableEntry {
  txId: number;
  status: TxnStatus;
  lastLSN: number;
}

export interface DirtyPageEntry {
  pageId: string;
  recLSN: number;
}

interface TxnRecord {
  lsn: number;
  txId: number;
  prevLSN: number;
}

export interface BeginRecord extends TxnRecord {
  type: 'BEGIN';
}

export interface UpdateRecord extends TxnRecord {
  type: 'UPDATE';
  pageId: string;
  before: number;
  after: number;
}

export interface ClrRecord extends TxnRecord {
  type: 'CLR';
  pageId: string;
  after: number;
  undoNextLSN: number;
}

export interface CommitRecord extends TxnRecord {
  type: 'COMMIT';
}

export interface AbortRecord extends TxnRecord {
  type: 'ABORT';
}

export interface EndRecord extends TxnRecord {
  type: 'END';
}

export interface CheckpointRecord {
  type: 'CHECKPOINT';
  lsn: number;
  txTable: TxnTableEntry[];
  dirtyPages: DirtyPageEntry[];
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | ClrRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | CheckpointRecord;

export type DistributiveOmit<T, K extends PropertyKey> = T extends unknown
  ? Omit<T, K>
  : never;

export type LogRecordInput = DistributiveOmit<LogRecord, 'lsn'>;
