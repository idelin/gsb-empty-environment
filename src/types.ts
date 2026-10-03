/**
 * ARIES 协议核心类型定义。
 * LSN 从 1 开始递增，0 表示 NULL_LSN（日志链起点）。
 */
export type LSN = number;
export type PageId = string;
export type TxId = number;

export const NULL_LSN: LSN = 0;

export type TxStatus = 'RUNNING' | 'COMMITTING' | 'ABORTING';

export interface TxTableEntry {
  txId: TxId;
  status: TxStatus;
  lastLSN: LSN;
}

/** 脏页表项：recLSN 为最早使该页变脏的日志 LSN */
export interface DirtyPageEntry {
  pageId: PageId;
  recLSN: LSN;
}

export interface UpdateRecord {
  type: 'UPDATE';
  lsn: LSN;
  txId: TxId;
  prevLSN: LSN;
  pageId: PageId;
  before: string;
  after: string;
}

/** 补偿日志记录：撤销某条 UPDATE，undoNextLSN 指向撤销链的下一个待处理 LSN */
export interface CLRRecord {
  type: 'CLR';
  lsn: LSN;
  txId: TxId;
  prevLSN: LSN;
  pageId: PageId;
  before: string;
  after: string;
  undoNextLSN: LSN;
}

export interface CommitRecord {
  type: 'COMMIT';
  lsn: LSN;
  txId: TxId;
  prevLSN: LSN;
}

export interface AbortRecord {
  type: 'ABORT';
  lsn: LSN;
  txId: TxId;
  prevLSN: LSN;
}

export interface EndRecord {
  type: 'END';
  lsn: LSN;
  txId: TxId;
  prevLSN: LSN;
}

export interface BeginCheckpointRecord {
  type: 'BEGIN_CHECKPOINT';
  lsn: LSN;
}

/** 模糊检查点：携带检查点开始时刻的事务表与脏页表快照 */
export interface EndCheckpointRecord {
  type: 'END_CHECKPOINT';
  lsn: LSN;
  beginLSN: LSN;
  transactionTable: TxTableEntry[];
  dirtyPageTable: DirtyPageEntry[];
}

export type LogRecord =
  | UpdateRecord
  | CLRRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;

export type TxLogRecord =
  | UpdateRecord
  | CLRRecord
  | CommitRecord
  | AbortRecord
  | EndRecord;

export interface PageImage {
  data: string;
  pageLSN: LSN;
}
