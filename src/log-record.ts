/**
 * ARIES WAL 日志记录类型定义。
 *
 * 不变量：
 *  - LSN 由单节点确定性时钟单调分配，严格递增、无间隙（从 1 开始）。
 *  - 每个事务的日志记录通过 prevLSN 构成反向链表。
 *  - CLR（补偿日志记录）携带 undoNextLSN，指向该事务下一个待撤销的记录，
 *    保证崩溃重启后 Undo 不会重复撤销已完成的工作。
 */

export type LSN = number;
export type TxnId = number;
export type PageId = string;

export const LogType = {
  Update: 'UPDATE',
  Clr: 'CLR',
  Commit: 'COMMIT',
  Abort: 'ABORT',
  End: 'END',
  BeginCheckpoint: 'BEGIN_CHECKPOINT',
  EndCheckpoint: 'END_CHECKPOINT',
} as const;

export type LogTypeName = (typeof LogType)[keyof typeof LogType];

export const TxnStatus = {
  Running: 'RUNNING',
  Committed: 'COMMITTED',
  Aborting: 'ABORTING',
} as const;

export type TxnStatusName = (typeof TxnStatus)[keyof typeof TxnStatus];

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatusName;
  lastLSN: LSN;
}

/** 事务表：Analysis 阶段重建，Undo 阶段的输入。 */
export type TransactionTable = Map<TxnId, TxnTableEntry>;

/** 脏页表：pageId -> recLSN（该页最近一次变脏的日志 LSN）。 */
export type DirtyPageTable = Map<PageId, LSN>;

interface BaseRecord {
  lsn: LSN;
  txnId: TxnId | null;
  prevLSN: LSN | null;
}

export interface UpdateRecord extends BaseRecord {
  type: typeof LogType.Update;
  pageId: PageId;
  before: number;
  after: number;
}

export interface ClrRecord extends BaseRecord {
  type: typeof LogType.Clr;
  pageId: PageId;
  /** 被补偿的更新在页面上的当前值（即原 UPDATE 的 after 映像）。 */
  before: number;
  /** 补偿后写入的值（即原 UPDATE 的 before 映像）。 */
  after: number;
  undoNextLSN: LSN | null;
}

export interface CommitRecord extends BaseRecord {
  type: typeof LogType.Commit;
}

export interface AbortRecord extends BaseRecord {
  type: typeof LogType.Abort;
}

export interface EndRecord extends BaseRecord {
  type: typeof LogType.End;
}

export interface BeginCheckpointRecord extends BaseRecord {
  type: typeof LogType.BeginCheckpoint;
  txnId: null;
  prevLSN: null;
}

export interface EndCheckpointRecord extends BaseRecord {
  type: typeof LogType.EndCheckpoint;
  txnId: null;
  prevLSN: null;
  txnTable: TxnTableEntry[];
  dirtyPageTable: Array<[PageId, LSN]>;
}

export type LogRecord =
  | UpdateRecord
  | ClrRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord;

/** 追加日志时的输入：除 lsn 外的完整记录。 */
export type LogRecordInput = Omit<LogRecord, 'lsn'>;

export function isPageModifying(
  record: LogRecord,
): record is UpdateRecord | ClrRecord {
  return record.type === LogType.Update || record.type === LogType.Clr;
}

export function serializeRecord(record: LogRecord): string {
  return JSON.stringify(record);
}

export function deserializeRecord(line: string): LogRecord {
  const parsed = JSON.parse(line) as LogRecord;
  if (typeof parsed.lsn !== 'number' || parsed.lsn < 1) {
    throw new Error(`corrupt WAL record: invalid lsn in ${line}`);
  }
  return parsed;
}

export function cloneTxnTable(table: TransactionTable): TransactionTable {
  const copy: TransactionTable = new Map();
  for (const [txnId, entry] of table) {
    copy.set(txnId, { ...entry });
  }
  return copy;
}
