/**
 * 变更数据捕获（CDC）事件模型。
 * 源库的 binlog/WAL 被解析为统一的 ChangeEvent，进入分流中间件。
 */

export type Row = Record<string, unknown>;

export type ChangeKind = 'insert' | 'update' | 'delete';

export interface ChangeEvent {
  kind: ChangeKind;
  /** 表名，例如 "user" */
  table: string;
  /** 主键值，例如 100 */
  key: unknown;
  /** 变更前镜像（update/delete 时存在） */
  before?: Row;
  /** 变更后镜像（insert/update 时存在） */
  after?: Row;
  /** 事件时间戳（毫秒） */
  ts: number;
}

/** 发送给冷存储（物理归档库）的归档包 */
export interface ArchivePackage {
  kind: 'archive-insert';
  table: string;
  key: unknown;
  /** 完整历史数据：删除时刻的整行快照 */
  record: Row;
  meta: {
    archivedAt: number;
    reason: 'logical-delete';
    sourceEventTs: number;
  };
}

/** 发送给只读查询库（搜索索引）的删除指令 */
export interface PurgeCommand {
  kind: 'readstore-delete';
  table: string;
  key: unknown;
}

export type RoutedCommand = ArchivePackage | PurgeCommand;
