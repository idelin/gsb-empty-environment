/**
 * Write-ahead log manager.
 *
 * Owns the deterministic logical clock that assigns LSNs. Records are
 * appended to an in-memory tail and become durable only after flush().
 * The durable prefix is persisted as JSON-lines "WAL file" content.
 */

import { serializeRecord, deserializeRecord } from './log-record.ts';
import type { LSN, LogRecord } from './log-record.ts';

export class WalManager {
  private records: LogRecord[] = [];
  private clock = 0; // deterministic single-node clock; last assigned LSN
  private flushedLSN: LSN = 0;

  /** Append a record, assigning the next LSN from the deterministic clock. */
  append<T extends Omit<LogRecord, 'lsn'> & { lsn?: LSN }>(rec: T): LogRecord {
    const lsn = ++this.clock;
    const full = { ...rec, lsn } as LogRecord;
    this.records.push(full);
    return full;
  }

  /** Flush all log records up to (and including) the given LSN to durable storage. */
  flush(uptoLSN: LSN): void {
    if (uptoLSN > this.clock) {
      throw new Error(`cannot flush beyond end of log: ${uptoLSN} > ${this.clock}`);
    }
    if (uptoLSN > this.flushedLSN) this.flushedLSN = uptoLSN;
  }

  getFlushedLSN(): LSN {
    return this.flushedLSN;
  }

  getLastLSN(): LSN {
    return this.clock;
  }

  /** All records (volatile view used during normal processing / recovery). */
  all(): readonly LogRecord[] {
    return this.records;
  }

  get(lsn: LSN): LogRecord {
    const rec = this.records[lsn - 1];
    if (!rec || rec.lsn !== lsn) throw new Error(`no record at LSN ${lsn}`);
    return rec;
  }

  /** Serialize only the durable (flushed) prefix — this is the WAL "file". */
  persistToFile(): string {
    return this.records
      .filter((r) => r.lsn <= this.flushedLSN)
      .map(serializeRecord)
      .join('\n');
  }

  /** Load a WAL file back into a fresh manager (simulates restart). */
  static loadFromFile(content: string): WalManager {
    const wal = new WalManager();
    const lines = content.split('\n').filter((l) => l.trim().length > 0);
    let prev = 0;
    for (const line of lines) {
      const rec = deserializeRecord(line);
      if (rec.lsn !== prev + 1) {
        throw new Error(`WAL file corrupt: LSN gap at ${rec.lsn} after ${prev}`);
      }
      prev = rec.lsn;
      wal.records.push(rec);
    }
    wal.clock = prev;
    wal.flushedLSN = prev; // everything in the file is durable
    return wal;
  }
}
