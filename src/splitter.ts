import type { ArchivePackage, ChangeEvent, PurgeCommand, Row } from './events.ts';
import { SinkRouter } from './router.ts';

export interface SplitterOptions {
  /** 需要拦截的表（缺省拦截所有表） */
  tables?: string[];
  /** 逻辑删除标记列名，默认 is_deleted */
  deletedFlag?: string;
  /**
   * 完整历史数据加载器：当 update 事件只携带变更列时，
   * 用它从源库补全整行快照，保证归档包包含完整历史数据。
   */
  rowLoader?: (table: string, key: unknown) => Row | undefined;
  /** 路由分发器 */
  router: SinkRouter;
}

export interface InterceptRecord {
  event: ChangeEvent;
  archivePackage: ArchivePackage;
  purgeCommand: PurgeCommand;
}

/** 业务下游处理器：分流器未拦截的事件原样透传给它 */
export type NextHandler = (event: ChangeEvent) => Promise<void> | void;

/**
 * 逻辑删除转物理归档流式分流器（业务逻辑标记处理中间件）。
 *
 * 拦截将 is_deleted 置为 1 的 Update 事件，自动转换为：
 *  - 下游物理归档库（冷存储）的 Insert（携带完整历史数据的归档包）
 *  - 只读查询库（搜索索引）的 Delete
 * 原始 Update 不再向下游传播，业务无感知。
 */
export class LogicalDeleteArchiveSplitter {
  readonly intercepted: InterceptRecord[] = [];
  private readonly tables?: Set<string>;
  private readonly deletedFlag: string;
  private readonly rowLoader?: (table: string, key: unknown) => Row | undefined;
  private readonly router: SinkRouter;

  constructor(options: SplitterOptions) {
    this.tables = options.tables ? new Set(options.tables) : undefined;
    this.deletedFlag = options.deletedFlag ?? 'is_deleted';
    this.rowLoader = options.rowLoader;
    this.router = options.router;
  }

  /** 判断事件是否为“逻辑删除”：update 且标记列由非 1 变为 1 */
  isLogicalDelete(event: ChangeEvent): boolean {
    if (event.kind !== 'update') return false;
    if (this.tables && !this.tables.has(event.table)) return false;
    const before = event.before?.[this.deletedFlag];
    const after = event.after?.[this.deletedFlag];
    return before !== 1 && after === 1;
  }

  /** 中间件入口：返回 true 表示事件已被拦截转换（不再透传） */
  async handle(event: ChangeEvent, next: NextHandler): Promise<boolean> {
    if (!this.isLogicalDelete(event)) {
      await next(event);
      return false;
    }

    const record = this.buildFullRecord(event);
    const archivePackage: ArchivePackage = {
      kind: 'archive-insert',
      table: event.table,
      key: event.key,
      record,
      meta: {
        archivedAt: Date.now(),
        reason: 'logical-delete',
        sourceEventTs: event.ts,
      },
    };
    const purgeCommand: PurgeCommand = {
      kind: 'readstore-delete',
      table: event.table,
      key: event.key,
    };

    this.intercepted.push({ event, archivePackage, purgeCommand });
    await this.router.dispatch(archivePackage);
    await this.router.dispatch(purgeCommand);
    return true;
  }

  /** 组装完整历史数据：优先 before 全量镜像，缺失时用 rowLoader 回源补全 */
  private buildFullRecord(event: ChangeEvent): Row {
    const merged: Row = { ...(event.before ?? {}), ...(event.after ?? {}) };
    if (this.rowLoader) {
      const full = this.rowLoader(event.table, event.key);
      if (full) return { ...full, ...merged };
    }
    return merged;
  }
}
