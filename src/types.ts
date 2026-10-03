export type LSN = number;
export type TxnId = number;
export type PageId = number;

export type TxnStatus = 'RUNNING' | 'COMMITTED' | 'ABORTING';

export interface TTEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN | null;
}

export interface UpdateRecord {
  lsn: LSN;
  type: 'UPDATE';
  txnId: TxnId;
  prevLSN: LSN | null;
  pageId: PageId;
  key: string;
  before: number;
  after: number;
}

export interface CLRRecord {
  lsn: LSN;
  type: 'CLR';
  txnId: TxnId;
  prevLSN: LSN | null;
  pageId: PageId;
  key: string;
  before: number;
  after: number;
  undoNextLSN: LSN | null;
}

export interface CommitRecord {
  lsn: LSN;
  type: 'COMMIT';
  txnId: TxnId;
  prevLSN: LSN | null;
}

export interface AbortRecord {
  lsn: LSN;
  type: 'ABORT';
  txnId: TxnId;
  prevLSN: LSN | null;
}

export interface EndRecord {
  lsn: LSN;
  type: 'END';
  txnId: TxnId;
  prevLSN: LSN | null;
}

export interface BeginCheckpointRecord {
  lsn: LSN;
  type: 'BEGIN_CHECKPOINT';
}

export interface EndCheckpointRecord {
  lsn: LSN;
  type: 'END_CHECKPOINT';
  beginLSN: LSN;
  transactionTable: TTEntry[];
  dirtyPageTable: Array<{ pageId: PageId; recLSN: LSN }>;
}

export type LogRecord =
  | UpdateRecord
  | CLRRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;
