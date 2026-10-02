export type LSN = number;
export type TxnId = number;
export type PageId = number;

export const NULL_LSN: LSN = 0;

export type TxnStatus = "running" | "committed" | "aborted";

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export type TxnTable = Map<TxnId, TxnTableEntry>;
export type DirtyPageTable = Map<PageId, LSN>;

export interface BeginRecord {
  lsn: LSN;
  type: "begin";
  txnId: TxnId;
  prevLSN: LSN;
}

export interface UpdateRecord {
  lsn: LSN;
  type: "update";
  txnId: TxnId;
  prevLSN: LSN;
  pageId: PageId;
  offset: number;
  before: string;
  after: string;
}

export interface CommitRecord {
  lsn: LSN;
  type: "commit";
  txnId: TxnId;
  prevLSN: LSN;
}

export interface AbortRecord {
  lsn: LSN;
  type: "abort";
  txnId: TxnId;
  prevLSN: LSN;
}

export interface EndRecord {
  lsn: LSN;
  type: "end";
  txnId: TxnId;
  prevLSN: LSN;
}

export interface ClrRecord {
  lsn: LSN;
  type: "clr";
  txnId: TxnId;
  prevLSN: LSN;
  undoNextLSN: LSN;
  pageId: PageId;
  offset: number;
  before: string;
  after: string;
}

export interface CheckpointRecord {
  lsn: LSN;
  type: "checkpoint";
  txnTable: TxnTableEntry[];
  dpt: [PageId, LSN][];
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | ClrRecord
  | CheckpointRecord;
