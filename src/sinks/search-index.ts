import type { PurgeCommand, RoutedCommand } from '../events.ts';
import type { Sink } from '../router.ts';

/**
 * 只读查询库（搜索索引）Sink。
 * 收到删除指令后按主键剔除文档，使已删除数据对查询不可见。
 */
export class SearchIndexSink implements Sink {
  readonly name = 'search-index';
  /** 模拟索引内容：key -> 文档 */
  readonly documents = new Map<unknown, Record<string, unknown>>();
  /** 已执行的删除指令（便于审计/断言） */
  readonly executedDeletes: PurgeCommand[] = [];

  apply(command: RoutedCommand): void {
    if (command.kind !== 'readstore-delete') {
      throw new Error(`search-index 不支持命令类型: ${command.kind}`);
    }
    this.executedDeletes.push(command);
    this.documents.delete(command.key);
  }
}
