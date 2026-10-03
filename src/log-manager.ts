import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { LogRecord, LSN } from './log-record.ts';

/**
 * Log manager: owns the in-memory log buffer and forces it to the WAL file
 * on demand. Enforces nothing by itself; the WAL protocol (log-before-data,
 * force-at-commit) is enforced by StorageEngine / TransactionManager.
 */
export class LogManager {
  /** All records ever appended; index `lsn - 1`. Survives only in memory. */
  private records: LogRecord[] = [];
  private flushedLSN: LSN = 0;
  private readonly filePath: string | null;

  constructor(filePath?: string, opts?: { truncate?: boolean }) {
    this.filePath = filePath ?? null;
    if (this.filePath && (opts?.truncate ?? false) && existsSync(this.filePath)) {
      writeFileSync(this.filePath, '');
    }
  }

  append(partial: Omit<LogRecord, 'lsn'>): LogRecord {
    const rec: LogRecord = { ...partial, lsn: this.records.length + 1 };
    this.records.push(rec);
    return rec;
  }

  getRecord(lsn: LSN): LogRecord {
    const rec = this.records[lsn - 1];
    if (!rec) throw new Error(`LogManager: no record with LSN ${lsn}`);
    return rec;
  }

  get lastLSN(): LSN {
    return this.records.length;
  }

  get flushedUpTo(): LSN {
    return this.flushedLSN;
  }

  /** Force every buffered record with lsn <= uptoLSN (default: all) to disk. */
  flush(uptoLSN?: LSN): void {
    const target = Math.min(uptoLSN ?? this.records.length, this.records.length);
    if (target <= this.flushedLSN) return;
    if (this.filePath) {
      const lines: string[] = [];
      for (let lsn = this.flushedLSN + 1; lsn <= target; lsn++) {
        lines.push(JSON.stringify(this.records[lsn - 1]));
      }
      appendFileSync(this.filePath, lines.join('\n') + '\n');
    }
    this.flushedLSN = target;
  }

  /** Seed the manager from records read back from a WAL file (recovery). */
  loadRecords(records: LogRecord[]): void {
    this.records = records.map((rec) => ({ ...rec }));
    this.flushedLSN = this.records.length;
  }

  static readFile(filePath: string): LogRecord[] {
    if (!existsSync(filePath)) return [];
    const text = readFileSync(filePath, 'utf8').trim();
    if (!text) return [];
    return text.split('\n').map((line) => JSON.parse(line) as LogRecord);
  }
}
