import type { LogEntry, TxnEvent } from './types.ts';

/**
 * 本地确定性日志：仅追加，seq 单调递增。
 * 状态机的一切输入都来自日志回放，快照记录切片点（已消费的日志位置），
 * 崩溃恢复 = 加载快照 + 从切片点继续回放。
 */
export class DeterministicLog {
  private entries: LogEntry[] = [];

  append(event: TxnEvent): LogEntry {
    const entry: LogEntry = { seq: this.entries.length, event };
    this.entries.push(entry);
    return entry;
  }

  get length(): number {
    return this.entries.length;
  }

  /** 当前切片点：小于该位置的日志可被快照覆盖。 */
  slicePoint(): number {
    return this.entries.length;
  }

  *replayFrom(seq: number): Iterable<LogEntry> {
    for (let i = seq; i < this.entries.length; i++) {
      yield this.entries[i]!;
    }
  }

  toJSON(): { entries: LogEntry[] } {
    return { entries: this.entries };
  }

  static fromJSON(data: { entries: LogEntry[] }): DeterministicLog {
    const log = new DeterministicLog();
    for (const entry of data.entries) {
      log.append(entry.event);
    }
    return log;
  }
}
