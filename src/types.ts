/** JSON 可序列化值（日志前/后镜像与页数据的取值范围）。 */
export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

/** 日志序列号（Log Sequence Number），单调递增，从 1 开始。 */
export type LSN = number;
export type TxId = number;
export type PageId = string;

export type TxStatus = 'running' | 'committed' | 'aborting';

/** 事务表（Transaction Table）条目。 */
export interface TxEntry {
  status: TxStatus;
  lastLSN: LSN;
}

/**
 * WAL 日志记录。
 * - UPDATE：更新前镜像 before / 后镜像 after（undefined 表示该键此前不存在）
 * - CLR：补偿日志记录，撤销时写入；undoNextLSN 指向下一条待撤销记录，保证撤销幂等
 * - CHECKPOINT：模糊检查点，携带事务表与脏页表（DPT）快照
 */
export type LogRecord =
  | { lsn: LSN; type: 'BEGIN'; txId: TxId; prevLSN: LSN }
  | {
      lsn: LSN;
      type: 'UPDATE';
      txId: TxId;
      prevLSN: LSN;
      pageId: PageId;
      key: string;
      before?: JsonValue;
      after?: JsonValue;
    }
  | {
      lsn: LSN;
      type: 'CLR';
      txId: TxId;
      prevLSN: LSN;
      pageId: PageId;
      key: string;
      after?: JsonValue;
      undoNextLSN: LSN;
    }
  | { lsn: LSN; type: 'COMMIT'; txId: TxId; prevLSN: LSN }
  | { lsn: LSN; type: 'ABORT'; txId: TxId; prevLSN: LSN }
  | { lsn: LSN; type: 'END'; txId: TxId; prevLSN: LSN }
  | {
      lsn: LSN;
      type: 'CHECKPOINT';
      txTable: Record<string, TxEntry>;
      dpt: Record<string, LSN>;
    };

type DistributiveOmit<T, K extends keyof T> = T extends unknown ? Omit<T, K> : never;

/** 追加日志时的入参（lsn 由 WAL 分配）。 */
export type LogRecordInput = DistributiveOmit<LogRecord, 'lsn'>;
