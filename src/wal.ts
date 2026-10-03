import { existsSync, readFileSync, appendFileSync, writeFileSync, renameSync } from 'node:fs';
import type { LogRecord } from './log-record.ts';

/**
 * Write-ahead log manager. Appends are staged in an in-memory buffer and only
 * become durable after flush(). A simulated crash therefore loses exactly the
 * unflushed tail, mirroring real WAL behavior.
 */
export class LogManager {
  private buffer: LogRecord[] = [];
  private nextLSN = 1;
  private durableLSN = 0;
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
    if (existsSync(filePath)) {
      for (const rec of LogManager.readFile(filePath)) {
        this.nextLSN = Math.max(this.nextLSN, rec.lsn + 1);
        this.durableLSN = Math.max(this.durableLSN, rec.lsn);
      }
    }
  }

  static readFile(filePath: string): LogRecord[] {
    if (!existsSync(filePath)) return [];
    const text = readFileSync(filePath, 'utf8');
    const records: LogRecord[] = [];
    for (const line of text.split('\n')) {
      if (line.trim().length > 0) records.push(JSON.parse(line) as LogRecord);
    }
    return records;
  }

  get lastLSN(): number {
    return this.nextLSN - 1;
  }

  get flushedLSN(): number {
    return this.durableLSN;
  }

  get bufferedCount(): number {
    return this.buffer.length;
  }

  /** Durable records followed by the still-unflushed tail. */
  scanIncludingBuffer(fromLSN = 1): LogRecord[] {
    return [...this.scan(fromLSN), ...this.buffer.filter((r) => r.lsn >= fromLSN)];
  }

  append(record: Omit<LogRecord, 'lsn'> & { lsn?: number }): LogRecord {
    const rec = { ...record, lsn: this.nextLSN++ } as LogRecord;
    this.buffer.push(rec);
    return rec;
  }

  /** Durably append every buffered record with lsn <= uptoLSN, preserving order. */
  flush(uptoLSN: number = Number.MAX_SAFE_INTEGER): void {
    const durable = this.buffer.filter((r) => r.lsn <= uptoLSN);
    if (durable.length === 0) return;
    appendFileSync(
      this.filePath,
      durable.map((r) => JSON.stringify(r)).join('\n') + '\n',
      'utf8',
    );
    this.buffer = this.buffer.filter((r) => r.lsn > uptoLSN);
    this.durableLSN = Math.max(this.durableLSN, durable[durable.length - 1].lsn);
  }

  /** Scan durable records with lsn >= fromLSN. */
  scan(fromLSN = 1): LogRecord[] {
    return LogManager.readFile(this.filePath).filter((r) => r.lsn >= fromLSN);
  }

  /** Drop the unflushed tail (simulated crash). */
  discardBuffer(): void {
    this.buffer = [];
    this.nextLSN = this.durableLSN + 1;
  }

  /**
   * Rewrite the durable log keeping only records with lsn >= keepFromLSN
   * (post-archive pruning). The unflushed buffer is left untouched.
   */
  truncateBefore(keepFromLSN: number): void {
    const kept = LogManager.readFile(this.filePath).filter((r) => r.lsn >= keepFromLSN);
    const tmp = this.filePath + '.tmp';
    writeFileSync(tmp, kept.map((r) => JSON.stringify(r)).join('\n') + (kept.length ? '\n' : ''), 'utf8');
    renameSync(tmp, this.filePath);
  }
}
