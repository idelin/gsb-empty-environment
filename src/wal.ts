import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { LogRecord, LSN } from './types.ts';

export interface WALStorage {
  appendBatch(records: LogRecord[]): void;
  readAll(): LogRecord[];
}

export class MemoryWALStorage implements WALStorage {
  private records: LogRecord[] = [];
  appendBatch(records: LogRecord[]): void {
    this.records.push(...records);
  }
  readAll(): LogRecord[] {
    return this.records.slice();
  }
}

// JSON-lines WAL file: one log record per line, append-only.
export class FileWALStorage implements WALStorage {
  readonly path: string;
  constructor(path: string, truncate = false) {
    this.path = path;
    if (truncate || !existsSync(path)) writeFileSync(path, '');
  }
  appendBatch(records: LogRecord[]): void {
    if (records.length === 0) return;
    appendFileSync(this.path, records.map((r) => JSON.stringify(r)).join('\n') + '\n');
  }
  readAll(): LogRecord[] {
    const text = readFileSync(this.path, 'utf8');
    return text
      .split('\n')
      .filter((line) => line.length > 0)
      .map((line) => JSON.parse(line) as LogRecord);
  }
}

// Log manager with a volatile in-memory tail. Records become durable only
// after flush(); a crash() drops the volatile tail, mimicking a power loss.
export class LogManager {
  private storage: WALStorage;
  private durableCache: LogRecord[];
  private tail: LogRecord[] = [];
  private nextLSN: LSN;

  constructor(storage: WALStorage) {
    this.storage = storage;
    this.durableCache = storage.readAll();
    this.nextLSN = this.durableCache.length;
  }

  get durableCount(): number {
    return this.durableCache.length;
  }

  get pendingTail(): number {
    return this.tail.length;
  }

  append(record: LogRecord): LogRecord {
    record.lsn = this.nextLSN++;
    this.tail.push(record);
    return record;
  }

  flush(uptoLSN?: LSN): void {
    const upto = uptoLSN ?? this.nextLSN - 1;
    const firstBeyond = this.tail.findIndex((r) => r.lsn > upto);
    const count = firstBeyond === -1 ? this.tail.length : firstBeyond;
    if (count === 0) return;
    const batch = this.tail.splice(0, count);
    this.storage.appendBatch(batch);
    this.durableCache.push(...batch);
  }

  read(lsn: LSN): LogRecord {
    if (lsn < this.durableCache.length) return this.durableCache[lsn];
    const record = this.tail[lsn - this.durableCache.length];
    if (!record) throw new Error(`LSN ${lsn} not found in WAL`);
    return record;
  }

  durable(): LogRecord[] {
    return this.durableCache.slice();
  }

  crash(): void {
    this.tail = [];
  }
}
