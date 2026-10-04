import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import type { LogRecord, LogRecordInput, LSN } from './types.ts';

/**
 * Write-ahead log backed by a JSON-lines file.
 *
 * Appends go to an in-memory buffer first; `flush()` forces them to stable
 * storage. A "crash" is simply everything that was never flushed being lost.
 */
export class WriteAheadLog {
  readonly filePath: string;
  private buffered: LogRecord[] = [];
  private nextLsn: LSN;

  constructor(filePath: string) {
    this.filePath = filePath;
    mkdirSync(dirname(filePath), { recursive: true });
    if (!existsSync(filePath)) writeFileSync(filePath, '');
    const existing = WriteAheadLog.read(filePath);
    this.nextLsn = existing.length === 0 ? 1 : existing[existing.length - 1].lsn + 1;
  }

  /** LSN of the most recently appended record (0 when the log is empty). */
  get currentLSN(): LSN {
    return this.nextLsn - 1;
  }

  /** Records appended but not yet forced to disk. */
  get bufferedCount(): number {
    return this.buffered.length;
  }

  append(input: LogRecordInput): LogRecord {
    const record = { ...input, lsn: this.nextLsn } as LogRecord;
    this.nextLsn += 1;
    this.buffered.push(record);
    return record;
  }

  flush(): void {
    if (this.buffered.length === 0) return;
    const chunk = this.buffered.map((r) => JSON.stringify(r) + '\n').join('');
    appendFileSync(this.filePath, chunk);
    this.buffered = [];
  }

  /** Read every record on stable storage, in LSN order. */
  static read(filePath: string): LogRecord[] {
    if (!existsSync(filePath)) return [];
    const text = readFileSync(filePath, 'utf8');
    const records: LogRecord[] = [];
    for (const line of text.split('\n')) {
      if (line.trim() === '') continue;
      records.push(JSON.parse(line) as LogRecord);
    }
    return records;
  }
}
