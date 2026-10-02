/** 变更操作类型 */
export type ChangeOp = 'INSERT' | 'UPDATE' | 'DELETE';

/** 单行变更记录 */
export interface ChangeRow {
  op: ChangeOp;
  table: string;
  pk: number;
  data: Record<string, unknown>;
}

/** BEGIN / END 标记分片的固定估算字节数 */
export const MARKER_BYTES = 64;

/** 事务起始标记分片 */
export interface BeginChunk {
  kind: 'BEGIN';
  txId: string;
  seq: number;
  byteSize: number;
}

/** 数据分片 */
export interface DataChunk {
  kind: 'DATA';
  txId: string;
  seq: number;
  rows: ChangeRow[];
  rowCount: number;
  /** 本分片内容的校验和 */
  checksum: string;
  byteSize: number;
}

/** 事务结束标记分片，携带全事务完整性信息 */
export interface EndChunk {
  kind: 'END';
  txId: string;
  seq: number;
  totalRows: number;
  totalChunks: number;
  /** 全事务滚动校验和 */
  checksum: string;
  byteSize: number;
}

export type Chunk = BeginChunk | DataChunk | EndChunk;
