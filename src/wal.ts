import * as fs from 'node:fs';
import * as path from 'node:path';
import { NULL_LSN, type LogRecord, type LSN } from './types.ts';

/**
 * 预写日志（WAL）：JSON Lines 持久化，追加写 + 同步落盘。
 * 内存中保留完整记录镜像，供 Undo 阶段按 LSN 随机访问。
 */
export class WriteAheadLog {
  private records: LogRecord[] = [];
  private nextLSN: LSN = 1;
  private readonly filePath: string;

  constructor(dir: string) {
    this.filePath = path.join(dir, 'wal.log');
    if (fs.existsSync(this.filePath)) {
      const lines = fs.readFileSync(this.filePath, 'utf8').split('\n');
      for (const line of lines) {
        if (line.trim() === '') continue;
        const rec = JSON.parse(line) as LogRecord;
        this.records.push(rec);
        this.nextLSN = rec.lsn + 1;
      }
    }
  }

  /** 追加一条日志并同步刷盘（WAL 先写协议的基础）。返回带 LSN 的完整记录。 */
  append<T extends Omit<LogRecord, 'lsn'>>(rec: T): T & { lsn: LSN } {
    const full = { ...rec, lsn: this.nextLSN } as T & { lsn: LSN };
    this.records.push(full as LogRecord);
    fs.appendFileSync(this.filePath, JSON.stringify(full) + '\n');
    this.nextLSN += 1;
    return full;
  }

  getRecord(lsn: LSN): LogRecord {
    if (lsn === NULL_LSN || lsn > this.records.length) {
      throw new Error(`invalid LSN: ${lsn}`);
    }
    return this.records[lsn - 1];
  }

  get lastLSN(): LSN {
    return this.nextLSN - 1;
  }

  get size(): number {
    return this.records.length;
  }

  /** 从 startLSN 到日志末尾的前向迭代器 */
  *scanFrom(startLSN: LSN): Generator<LogRecord> {
    for (let lsn = Math.max(1, startLSN); lsn <= this.records.length; lsn++) {
      yield this.records[lsn - 1];
    }
  }

  *scanAll(): Generator<LogRecord> {
    yield* this.scanFrom(1);
  }
}
