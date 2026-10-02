import type { LogRecord, LSN } from './types.ts';
import { NULL_LSN } from './types.ts';

export class LogManager {
  private buffer: LogRecord[] = [];
  private stable: LogRecord[] = [];
  private nextLSN: LSN = 1;
  flushedLSN: LSN = NULL_LSN;

  append(partial: Omit<LogRecord, 'lsn'>): LogRecord {
    const rec: LogRecord = { ...partial, lsn: this.nextLSN++ };
    this.buffer.push(rec);
    return rec;
  }

  flush(uptoLSN?: LSN): void {
    const limit = uptoLSN ?? Number.MAX_SAFE_INTEGER;
    let i = 0;
    while (i < this.buffer.length && this.buffer[i].lsn <= limit) {
      this.stable.push(this.buffer[i]);
      this.flushedLSN = this.buffer[i].lsn;
      i++;
    }
    this.buffer = this.buffer.slice(i);
  }

  force(): void {
    this.flush();
  }

  crash(): void {
    this.buffer = [];
    this.nextLSN = this.stable.length ? this.stable[this.stable.length - 1].lsn + 1 : 1;
  }

  stableRecords(): readonly LogRecord[] {
    return this.stable;
  }

  allRecords(): LogRecord[] {
    return this.stable.concat(this.buffer);
  }

  get(lsn: LSN): LogRecord | undefined {
    if (lsn >= 1 && lsn <= this.stable.length) return this.stable[lsn - 1];
    const idx = lsn - this.stable.length - 1;
    if (idx >= 0 && idx < this.buffer.length) return this.buffer[idx];
    return undefined;
  }

  get bufferedCount(): number {
    return this.buffer.length;
  }

  get stableCount(): number {
    return this.stable.length;
  }
}
