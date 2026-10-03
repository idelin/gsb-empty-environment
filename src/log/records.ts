/**
 * ARIES WAL 日志记录类型定义。
 * LSN 为单调递增的 64 位语义整数（实现上用 number，测试规模足够）。
 */

export type LSN = number;
export type TxnId = number;
export type PageId = string;

export type LogRecordType =
  | 'BEGIN'
  | 'UPDATE'
  | 'COMMIT'
  | 'ABORT'
  | 'END'
  | 'CLR'
  | 'BEGIN_CHECKPOINT'
  | 'END_CHECKPOINT';

export interface BaseRecord {
  lsn: LSN;
  prevLSN: LSN; // 同一事务的前一条日志，0 表示无
  txnId: TxnId; // 0 表示与事务无关（checkpoint）
  type: LogRecordType;
}

export interface BeginRecord extends BaseRecord {
  type: 'BEGIN';
}

export interface UpdateRecord extends BaseRecord {
  type: 'UPDATE';
  pageId: PageId;
  offset: number;
  beforeImage: string;
  afterImage: string;
}

export interface CommitRecord extends BaseRecord {
  type: 'COMMIT';
}

export interface AbortRecord extends BaseRecord {
  type: 'ABORT';
}

export interface EndRecord extends BaseRecord {
  type: 'END';
}

/** 补偿日志：undo 时写入，redo 阶段重复历史时同样重放 */
export interface ClrRecord extends BaseRecord {
  type: 'CLR';
  pageId: PageId;
  offset: number;
  /** CLR 只携带重做所需的 after-image（即被补偿记录的 before-image） */
  afterImage: string;
  /** 指向下一条需要 undo 的记录 LSN，跳过已补偿部分 */
  undoNextLSN: LSN;
}

export interface BeginCheckpointRecord extends BaseRecord {
  type: 'BEGIN_CHECKPOINT';
}

export interface EndCheckpointRecord extends BaseRecord {
  type: 'END_CHECKPOINT';
  /** 模糊检查点：快照当时的活跃事务表与脏页表 */
  txnTable: Array<{ txnId: TxnId; status: TxnStatus; lastLSN: LSN }>;
  dirtyPageTable: Array<{ pageId: PageId; recLSN: LSN }>;
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | ClrRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;

export type TxnStatus = 'RUNNING' | 'COMMITTING' | 'ABORTING' | 'ENDED';

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface DirtyPageEntry {
  pageId: PageId;
  recLSN: LSN;
}
