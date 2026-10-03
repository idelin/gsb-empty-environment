import { closeSync, existsSync, fsyncSync, openSync, readFileSync, writeSync } from 'node:fs';
import type { LogRecord, LogRecordInput } from './types.ts';

export class WalWriter {
  readonly records: LogRecord[] = [];
  private readonly fd: number;
  private nextLSN: number;
  private isClosed = false;

  private constructor(fd: number, startLSN: number) {
    this.fd = fd;
    this.nextLSN = startLSN;
  }

  static create(path: string): WalWriter {
    return new WalWriter(openSync(path, 'w'), 1);
  }

  static openAppend(path: string, existing: LogRecord[]): WalWriter {
    const maxLSN = existing.reduce((max, rec) => Math.max(max, rec.lsn), 0);
    const writer = new WalWriter(openSync(path, 'a'), maxLSN + 1);
    writer.records.push(...existing);
    return writer;
  }

  append(input: LogRecordInput): LogRecord {
    if (this.isClosed) throw new Error('WAL writer is closed');
    const record = { ...input, lsn: this.nextLSN++ } as LogRecord;
    this.records.push(record);
    writeSync(this.fd, JSON.stringify(record) + '\n');
    return record;
  }

  recordAt(lsn: number): LogRecord {
    const record = this.records[lsn - 1];
    if (!record || record.lsn !== lsn) throw new Error(`no log record at LSN ${lsn}`);
    return record;
  }

  flush(): void {
    if (!this.isClosed) fsyncSync(this.fd);
  }

  close(): void {
    if (this.isClosed) return;
    this.flush();
    closeSync(this.fd);
    this.isClosed = true;
  }

  get closed(): boolean {
    return this.isClosed;
  }

  get currentLSN(): number {
    return this.nextLSN;
  }
}

export function readWal(path: string): LogRecord[] {
  if (!existsSync(path)) return [];
  return readFileSync(path, 'utf8')
    .split('\n')
    .filter((line) => line.trim().length > 0)
    .map((line) => JSON.parse(line) as LogRecord);
}
