/**
 * Migrator：把“选路 -> 逐步执行 -> 断点持久化 -> 恢复”串起来的门面。
 *
 * 发布规则：只有全部步骤成功（status='completed'）才返回并清除断点；
 * 任何失败都抛 MigrationError，断点保留在最后一个成功步骤（不发布新版本）。
 */
import type { MigrationGraph } from './graph.ts';
import type { CheckpointStore } from './store.ts';
import { migrate, resume } from './migrate.ts';
import type { Envelope, MigrationResult } from './migrate.ts';
import { MigrationError } from './errors.ts';

export interface MigratorOptions {
  /** 每完成一步持久化断点前调用；抛错可模拟“崩溃”（断点已保存则可恢复）。 */
  onPersist?: (checkpointIndex: number) => void;
}

export class Migrator {
  private readonly graph: MigrationGraph;
  private readonly store: CheckpointStore;

  constructor(graph: MigrationGraph, store: CheckpointStore) {
    this.graph = graph;
    this.store = store;
  }

  /** 启动一次新迁移；存在未完成断点时拒绝，避免误覆盖恢复点。 */
  run(input: Envelope, targetVersion: string, options: MigratorOptions = {}): MigrationResult {
    const existing = this.store.load();
    if (existing && existing.status === 'in-progress') {
      throw new MigrationError({
        code: 'CHECKPOINT_EXECUTION_MISMATCH',
        message:
          'an in-progress checkpoint already exists; call resume() with the original input, or clear the store to start over',
      });
    }
    if (existing && existing.status === 'completed') {
      this.store.clear();
    }

    const result = migrate(this.graph, input, targetVersion, {
      onCheckpoint: (checkpoint) => {
        this.store.save(checkpoint);
        options.onPersist?.(checkpoint.executed.length - 1);
      },
    });

    // 全部成功才算发布：清除断点，返回新版本 Envelope。
    this.store.clear();
    return result;
  }

  /** 从断点恢复：必须提供与当初完全一致的原始输入。 */
  recover(originalInput: Envelope, options: MigratorOptions = {}): MigrationResult {
    const checkpoint = this.store.load();
    if (checkpoint === null) {
      throw new MigrationError({
        code: 'CHECKPOINT_NOT_FOUND',
        message: 'no checkpoint to resume from',
      });
    }
    if (checkpoint.status === 'completed') {
      this.store.clear();
    }

    const result = resume(this.graph, checkpoint, originalInput, {
      onCheckpoint: (updated) => {
        this.store.save(updated);
        options.onPersist?.(updated.executed.length - 1);
      },
    });

    this.store.clear();
    return result;
  }

  checkpoint() {
    return this.store.load();
  }
}
