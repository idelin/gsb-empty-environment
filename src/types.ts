export type LSN = number;
export type TxnId = number;
export type PageId = string;

export const NULL_LSN: LSN = 0;

export interface BeginRecord {
  type: "BEGIN";
  lsn: LSN;
  txnId: TxnId;
  prevLSN: LSN;
}

export interface UpdateRecord {
  type: "UPDATE";
  lsn: LSN;
  txnId: TxnId;
  pageId: PageId;
  before: string;
  after: string;
  prevLSN: LSN;
}

export interface ClrRecord {
  type: "CLR";
  lsn: LSN;
  txnId: TxnId;
  pageId: PageId;
  after: string; // redo image of the undo (== before image of the undone update)
  prevLSN: LSN;
  undoNextLSN: LSN; // next record of this txn still to undo (== undone update's prevLSN)
  undidLSN: LSN; // LSN of the UPDATE this CLR compensates
}

export interface SavepointRecord {
  type: "SAVEPOINT";
  lsn: LSN;
  txnId: TxnId;
  name: string;
  savepointLSN: LSN; // txn-internal LSN the savepoint is bound to
  prevLSN: LSN;
}

export interface CommitRecord {
  type: "COMMIT";
  lsn: LSN;
  txnId: TxnId;
  prevLSN: LSN;
}

export interface AbortRecord {
  type: "ABORT";
  lsn: LSN;
  txnId: TxnId;
  prevLSN: LSN;
}

export interface EndRecord {
  type: "END";
  lsn: LSN;
  txnId: TxnId;
  prevLSN: LSN;
}

export interface DirtyPageEntry {
  pageId: PageId;
  recLSN: LSN;
}

export interface ActiveTxnEntry {
  txnId: TxnId;
  firstLSN: LSN;
  lastLSN: LSN;
  status: "running" | "aborting";
}

export interface CkptBeginRecord {
  type: "CKPT_BEGIN";
  lsn: LSN;
  ckptId: number;
}

export interface CkptEndRecord {
  type: "CKPT_END";
  lsn: LSN;
  ckptId: number;
  dirtyPages: DirtyPageEntry[];
  activeTxns: ActiveTxnEntry[];
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | ClrRecord
  | SavepointRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | CkptBeginRecord
  | CkptEndRecord;

export type TxnStatus = "running" | "committed" | "aborting" | "ended";
