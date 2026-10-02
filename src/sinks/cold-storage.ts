import type { ArchivePackage, RoutedCommand } from '../events.ts';
import type { Sink } from '../router.ts';

/**
 * 物理归档库（冷存储）Sink。
 * 以归档包形式持久化完整历史数据，供审计与回溯。
 */
export class ColdStorageSink implements Sink {
  readonly name = 'cold-storage';
  /** 已入库的归档包 */
  readonly archives: ArchivePackage[] = [];

  apply(command: RoutedCommand): void {
    if (command.kind !== 'archive-insert') {
      throw new Error(`cold-storage 不支持命令类型: ${command.kind}`);
    }
    this.archives.push(command);
  }

  find(table: string, key: unknown): ArchivePackage | undefined {
    return this.archives.find((a) => a.table === table && a.key === key);
  }
}
