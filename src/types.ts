/**
 * ARIES 协议核心类型定义。
 * LSN 从 1 开始单调递增，NULL_LSN 表示空指针。
 */
export type LSN = number;
export const NULL_LSN: LSN = 0;

export type PageId = string;
export type TxnId = string;

export type TxnStatus = 'running' | 'committed' | 'aborting';

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface DirtyPageEntry {
  pageId: PageId;
  recLSN: LSN;
}

interface BaseRecord {
  lsn: LSN;
  txnId: TxnId | null;
  prevLSN: LSN;
}

export interface UpdateRecord extends BaseRecord {
  type: 'UPDATE';
  txnId: TxnId;
  pageId: PageId;
  before: number;
  after: number;
}

/** 补偿日志记录：撤销某个 UPDATE 时写入，undoNextLSN 指向该事务下一个待撤销记录 */
export interface ClrRecord extends BaseRecord {
  type: 'CLR';
  txnId: TxnId;
  pageId: PageId;
  before: number;
  after: number;
  undoNextLSN: LSN;
}

export interface CommitRecord extends BaseRecord {
  type: 'COMMIT';
  txnId: TxnId;
}

export interface AbortRecord extends BaseRecord {
  type: 'ABORT';
  txnId: TxnId;
}

export interface EndRecord extends BaseRecord {
  type: 'END';
  txnId: TxnId;
}

export interface BeginCheckpointRecord extends BaseRecord {
  type: 'BEGIN_CHECKPOINT';
  txnId: null;
}

export interface EndCheckpointRecord extends BaseRecord {
  type: 'END_CHECKPOINT';
  txnId: null;
  transactions: TxnTableEntry[];
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

export type TxnLogRecord = Exclude<LogRecord, BeginCheckpointRecord | EndCheckpointRecord>;
