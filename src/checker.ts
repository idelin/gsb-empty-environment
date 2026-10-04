import { SourceNode } from './source';
import { TargetNode } from './target';
import { Key, Op } from './types';

export class CheckError extends Error {}

/**
 * 一致性检查器。
 * - 逐步检查（checkStep）：每一轮复制后，乙持有的每个条目都必须与甲 oplog 中
 *   同 seq 的已接受写完全一致；墓碑不可见（删除不复活）。
 * - 最终检查（checkFinal）：切换完成后，每个已确认写的最终效果在乙可见，
 *   且乙不多出任何未确认的 key。
 */
export class Checker {
  private lastAckedByKey = new Map<Key, Op>();
  private ackedCount = 0;
  private rejectedCount = 0;

  constructor(private readonly source: SourceNode) {}

  recordAck(op: Op): void {
    // 并发写可能乱序收到响应，客户端模型只保留每个 key 最大 seq 的已确认写
    const cur = this.lastAckedByKey.get(op.key);
    if (!cur || cur.seq < op.seq) {
      this.lastAckedByKey.set(op.key, op);
    }
    this.ackedCount++;
  }

  recordReject(): void {
    this.rejectedCount++;
  }

  get stats(): { acked: number; rejected: number } {
    return { acked: this.ackedCount, rejected: this.rejectedCount };
  }

  /** 每一步复制（基础/增量/排空）之后调用 */
  checkStep(target: TargetNode, label: string): void {
    for (const key of target.keys()) {
      const entry = target.rawEntry(key)!;
      const op = this.source.opBySeq(entry.seq);
      if (!op || op.key !== key) {
        throw new CheckError(`[${label}] 乙的 ${key}@seq=${entry.seq} 在甲的 oplog 中不存在`);
      }
      const expected = op.kind === 'del' ? null : op.value;
      if (entry.value !== expected) {
        throw new CheckError(
          `[${label}] 乙的 ${key}@seq=${entry.seq} 值=${String(entry.value)}，应为 ${String(expected)}`,
        );
      }
      if (entry.value === null && target.visibleGet(key) !== undefined) {
        throw new CheckError(`[${label}] 已删除的 ${key} 在乙复活了`);
      }
    }
  }

  /** 切换完成（全部增量已排空）之后调用 */
  checkFinal(target: TargetNode): void {
    for (const [key, op] of this.lastAckedByKey) {
      const entry = target.rawEntry(key);
      if (!entry) {
        throw new CheckError(`已确认写 ${key}@seq=${op.seq} 在乙不可见（条目缺失）`);
      }
      if (entry.seq !== op.seq) {
        throw new CheckError(`已确认写 ${key}@seq=${op.seq} 在乙不可见（乙停留在 seq=${entry.seq}）`);
      }
      const visible = target.visibleGet(key);
      const expected = op.kind === 'del' ? undefined : (op.value as string);
      if (visible !== expected) {
        throw new CheckError(
          `已确认写 ${key}@seq=${op.seq} 的最终效果不符：乙可见=${String(visible)}，应为 ${String(expected)}`,
        );
      }
    }
    for (const key of target.keys()) {
      if (!this.lastAckedByKey.has(key)) {
        throw new CheckError(`乙多出了未确认的 key：${key}`);
      }
    }
  }
}
