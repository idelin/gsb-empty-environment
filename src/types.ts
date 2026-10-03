/** 行镜像：列名 -> 列值 */
export type RowImage = Record<string, unknown>;

export type CdcOp = 'INSERT' | 'UPDATE' | 'DELETE';

/** 一条 CDC 变更事件，携带前置/后置镜像 */
export interface CdcEvent {
  /** 日志全局单调序号 */
  sequence: number;
  /** 所属事务 ID */
  txId: string;
  /** 变更发生的毫秒级时间戳（可含小数，精度高于 1ms） */
  timestampMs: number;
  op: CdcOp;
  table: string;
  /** 主键列镜像 */
  primaryKey: RowImage;
  /** 前置镜像（INSERT 时为 null） */
  before: RowImage | null;
  /** 后置镜像（DELETE 时为 null） */
  after: RowImage | null;
}

export type DmlKind = 'INSERT' | 'UPDATE' | 'DELETE';

/** 一条补偿（Undo）DML */
export interface DmlStatement {
  kind: DmlKind;
  table: string;
  /** UPDATE 的 SET 子句 */
  set?: RowImage;
  /** INSERT 的 VALUES */
  values?: RowImage;
  /** WHERE 条件（主键等值） */
  where?: RowImage;
  /** 来源 CDC 事件序号，便于审计追溯 */
  sourceSequence: number;
}
