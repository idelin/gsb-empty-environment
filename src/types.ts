export type Row = Record<string, unknown>;

export type ChangeKind = 'insert' | 'update' | 'delete';

/** 源库 CDC 变更事件 */
export interface ChangeEvent {
  kind: ChangeKind;
  table: string;
  keyColumn: string;
  primaryKey: string | number;
  before?: Row;
  after?: Row;
  source: string;
  occurredAt: string;
}

/** 分流器产出、发往异构下游的动作 */
export type RoutedAction =
  | { type: 'insert'; sink: string; table: string; record: Row }
  | { type: 'delete'; sink: string; table: string; id: string | number };

export interface DispatchReceipt {
  action: RoutedAction;
  status: 'ok' | 'failed';
  error?: string;
}

/** 异构存储系统的统一适配接口（搜索索引 / 冷存储 / 只读查询库……） */
export interface Sink {
  readonly name: string;
  insert(table: string, record: Row): Promise<void>;
  delete(table: string, id: string | number): Promise<void>;
}

/** 业务逻辑标记处理中间件 */
export interface Middleware {
  readonly name: string;
  handle(event: ChangeEvent, emit: (action: RoutedAction) => void): void | Promise<void>;
}
