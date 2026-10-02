import fs from 'node:fs';
import path from 'node:path';
import type { LogRecord, LogRecordInput, LSN } from './types.ts';

/**
 * 预写日志（Write-Ahead Log）。
 *
 * - append() 同步写入 OS（进程崩溃不丢），但不 fsync；
 * - flush() 执行 fsync，保证日志在断电后依然持久；
 * - 提交（COMMIT）与脏页刷盘前必须调用 flush()，以满足 WAL 协议：
 *   “页面落盘之前，描述其修改的日志必须先持久化”。
 *
 * 日志文件为 JSON Lines，每行一条记录，LSN 即记录序号（从 1 开始）。
 */
export class Wal {
  private fd: number;
  private records: LogRecord[] = [];
  /** 已确保持久化（fsync）的最大 LSN。 */
  flushedLSN: LSN = 0;
  readonly file: string;

  constructor(file: string) {
    this.file = file;
    fs.mkdirSync(path.dirname(file), { recursive: true });
    if (fs.existsSync(file)) {
      const text = fs.readFileSync(file, 'utf8');
      for (const line of text.split('\n')) {
        if (!line.trim()) continue;
        const rec = JSON.parse(line) as LogRecord;
        this.records[rec.lsn - 1] = rec;
      }
    }
    this.fd = fs.openSync(file, 'a');
    this.flushedLSN = this.records.length;
  }

  get lastLSN(): LSN {
    return this.records.length;
  }

  /** 追加一条日志记录并同步写入 OS 缓冲（fsync 由 flush 负责）。 */
  append(input: LogRecordInput): LogRecord {
    const rec = { ...input, lsn: this.records.length + 1 } as LogRecord;
    this.records.push(rec);
    fs.writeSync(this.fd, JSON.stringify(rec) + '\n');
    return rec;
  }

  /** fsync：确保 uptoLSN 之前的所有日志记录在断电后依然持久。 */
  flush(uptoLSN: LSN = this.lastLSN): void {
    fs.fsyncSync(this.fd);
    this.flushedLSN = Math.max(this.flushedLSN, uptoLSN);
  }

  get(lsn: LSN): LogRecord {
    const rec = this.records[lsn - 1];
    if (!rec) throw new Error(`WAL: no log record at LSN ${lsn}`);
    return rec;
  }

  *scan(fromLSN: LSN = 1): Generator<LogRecord> {
    for (let lsn = Math.max(1, fromLSN); lsn <= this.records.length; lsn++) {
      yield this.records[lsn - 1];
    }
  }

  /** 最近一条 CHECKPOINT 记录的 LSN，没有则返回 0。 */
  lastCheckpointLSN(): LSN {
    for (let lsn = this.records.length; lsn >= 1; lsn--) {
      if (this.records[lsn - 1].type === 'CHECKPOINT') return lsn;
    }
    return 0;
  }

  close(): void {
    this.flush();
    fs.closeSync(this.fd);
  }
}
