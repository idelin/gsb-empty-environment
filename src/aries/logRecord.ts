/**
 * ARIES WAL 日志记录定义。
 * LSN 为单调递增的整数。
 */

export type LSN = number;
export type TxnId = number;
export type PageId = number;

export const LogType = {
  UPDATE: "UPDATE",
  COMMIT: "COMMIT",
  ABORT: "ABORT",
  END: "END",
  CLR: "CLR",
  CHECKPOINT: "CHECKPOINT",
} as const;
export type LogTypeName = (typeof LogType)[keyof typeof LogType];

export const TxnStatus = {
  RUNNING: "RUNNING",
  COMMITTED: "COMMITTED",
  ABORTING: "ABORTING",
} as const;
export type TxnStatusName = (typeof TxnStatus)[keyof typeof TxnStatus];

/** 事务表条目（Analysis 阶段重建） */
export interface TransactionTableEntry {
  txnId: TxnId;
  status: TxnStatusName;
  lastLSN: LSN;
}

/** 脏页表条目：recLSN 为该页首次变脏的日志 LSN */
export interface DirtyPageTableEntry {
  pageId: PageId;
  recLSN: LSN;
}

export type TransactionTable = Map<TxnId, TransactionTableEntry>;
export type DirtyPageTable = Map<PageId, DirtyPageTableEntry>;

export interface CheckpointPayload {
  transactionTable: TransactionTableEntry[];
  dirtyPageTable: DirtyPageTableEntry[];
}

export interface LogRecord {
  lsn: LSN;
  /** 同一事务的前一条日志 LSN；无则为 null */
  prevLSN: LSN | null;
  txnId: TxnId;
  type: LogTypeName;
  pageId?: PageId;
  /** UPDATE：修改前镜像 */
  before?: string;
  /** UPDATE：修改后镜像 */
  after?: string;
  /** CLR 专用：撤销完成后下一条待处理的 LSN */
  undoNextLSN?: LSN | null;
  /** CHECKPOINT 负载 */
  checkpoint?: CheckpointPayload;
}

/** 序列化（写入 WAL 文件，JSON Lines） */
export function serializeRecord(rec: LogRecord): string {
  return JSON.stringify(rec);
}

export function deserializeRecord(line: string): LogRecord {
  const obj = JSON.parse(line) as LogRecord;
  if (typeof obj.lsn !== "number" || typeof obj.txnId !== "number" || !obj.type) {
    throw new Error(`非法日志记录: ${line}`);
  }
  return obj;
}
