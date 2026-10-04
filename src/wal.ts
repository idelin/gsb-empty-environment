import * as fs from 'node:fs';
import type { LogRecord, LSN } from './types.ts';

/**
 * 预写日志（WAL）文件：JSON Lines 持久化。
 * 追加操作通过内部 promise 链串行化，保证并发调用下 LSN 分配
 * 与落盘顺序全局一致（单一日志流是 ARIES 的核心假设）。
 */
export class Wal {
  private nextLSN: LSN;
  private queue: Promise<unknown> = Promise.resolve();
  /** 已追加记录的内存镜像，供恢复与链式遍历使用 */
  readonly records: LogRecord[] = [];

  private readonly filePath: string;

  private constructor(filePath: string, startLSN: LSN) {
    this.filePath = filePath;
    this.nextLSN = startLSN;
  }

  /** 创建新 WAL 文件（截断已有内容） */
  static create(filePath: string): Wal {
    fs.writeFileSync(filePath, '');
    return new Wal(filePath, 1);
  }

  /** 崩溃后重开 WAL：从文件恢复 LSN 计数器与内存镜像 */
  static resume(filePath: string): Wal {
    const records = readWal(filePath);
    const wal = new Wal(filePath, records.length === 0 ? 1 : records[records.length - 1].lsn + 1);
    wal.records.push(...records);
    return wal;
  }

  /** 追加单条记录；make 回调收到分配好的 LSN，必须原样回填 */
  append<R extends LogRecord>(make: (lsn: LSN) => R): Promise<R> {
    const task = this.queue.then(() => {
      const record = make(this.nextLSN);
      if (record.lsn !== this.nextLSN) {
        throw new Error(`WAL LSN 分配不一致: 期望 ${this.nextLSN}, 实际 ${record.lsn}`);
      }
      this.persist(record);
      return record;
    });
    this.queue = task.catch(() => undefined);
    return task;
  }

  /** 原子追加一组记录（用于 BEGIN/END_CHECKPOINT 对，保证不被并发追加打断） */
  appendAtomic<R extends LogRecord>(make: (firstLSN: LSN) => R[]): Promise<R[]> {
    const task = this.queue.then(() => {
      const records = make(this.nextLSN);
      records.forEach((record, i) => {
        if (record.lsn !== this.nextLSN + i) {
          throw new Error(`WAL LSN 分配不一致: 期望 ${this.nextLSN + i}, 实际 ${record.lsn}`);
        }
      });
      for (const record of records) this.persist(record);
      return records;
    });
    this.queue = task.catch(() => undefined);
    return task;
  }

  private persist(record: LogRecord): void {
    fs.appendFileSync(this.filePath, JSON.stringify(record) + '\n');
    this.records.push(record);
    this.nextLSN = record.lsn + 1;
  }

  /** 等待所有排队追加完成（无泄漏断言用：确认无悬挂 I/O） */
  drain(): Promise<void> {
    return this.queue.then(() => undefined);
  }

  get currentLSN(): LSN {
    return this.nextLSN - 1;
  }
}

export function readWal(filePath: string): LogRecord[] {
  const text = fs.readFileSync(filePath, 'utf8');
  return text
    .split('\n')
    .filter((line) => line.trim().length > 0)
    .map((line) => JSON.parse(line) as LogRecord);
}
