import type { ChangeEvent, Middleware, RoutedAction, Row } from './types.ts';

export interface LogicalDeleteOptions {
  /** 逻辑删除标记列，默认 is_deleted */
  flagColumn?: string;
  /** 物理归档库（冷存储）sink 名称 */
  archiveSink?: string;
  /** 需要同步删除的只读侧 sink 名称列表（搜索索引、只读查询库……） */
  readSinks?: string[];
  /** 归档包写入的目标表名，默认 archive_<原表名> */
  archiveTable?: (table: string) => string;
}

function isDeletedFlag(value: unknown): boolean {
  return value === 1 || value === true || value === '1';
}

/**
 * 逻辑删除标记中间件：
 * 拦截 is_deleted 由非 1 变为 1 的 Update 事件，转换为
 *  - 下游物理归档库的 Insert（携带完整历史数据的归档包）
 *  - 只读查询库 / 搜索索引的 Delete
 */
export class LogicalDeleteArchiveMiddleware implements Middleware {
  readonly name = 'logical-delete-archive';
  private readonly flagColumn: string;
  private readonly archiveSink: string;
  private readonly readSinks: string[];
  private readonly archiveTable: (table: string) => string;

  constructor(options: LogicalDeleteOptions = {}) {
    this.flagColumn = options.flagColumn ?? 'is_deleted';
    this.archiveSink = options.archiveSink ?? 'cold-storage';
    this.readSinks = options.readSinks ?? ['search-index', 'readonly-query'];
    this.archiveTable = options.archiveTable ?? ((table) => `archive_${table}`);
  }

  handle(event: ChangeEvent, emit: (action: RoutedAction) => void): void {
    if (event.kind !== 'update') return;
    const beforeFlag = event.before?.[this.flagColumn];
    const afterFlag = event.after?.[this.flagColumn];
    // 仅响应“逻辑删除发生”的瞬间：非删除 -> 删除
    if (isDeletedFlag(beforeFlag) || !isDeletedFlag(afterFlag)) return;

    const archivePackage: Row = {
      ...(event.after as Row),
      _archive_meta: {
        source: event.source,
        sourceTable: event.table,
        keyColumn: event.keyColumn,
        primaryKey: event.primaryKey,
        deleteKind: 'logical',
        archivedAt: new Date().toISOString(),
        sourceOccurredAt: event.occurredAt,
      },
    };

    emit({
      type: 'insert',
      sink: this.archiveSink,
      table: this.archiveTable(event.table),
      record: archivePackage,
    });
    for (const sink of this.readSinks) {
      emit({ type: 'delete', sink, table: event.table, id: event.primaryKey });
    }
  }
}
