/**
 * Write-ahead log: an append-only JSONL file guarded by an async mutex
 * (promise tail-queue) so concurrent writers can never interleave a single
 * record or reuse an LSN. LSNs are assigned strictly sequentially and each
 * record's prevLSN is chained to the previous record of the same transaction.
 */

import { appendFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { parseRecord, serializeRecord } from './log-record.ts';
import type { LogRecord, LogRecordDraft, LSN, TxnId } from './log-record.ts';

export class Wal {
  readonly path: string;
  private records: LogRecord[] = [];
  private nextLSN: LSN = 1;
  private lastLSNByTxn = new Map<TxnId, LSN>();
  private tail: Promise<unknown> = Promise.resolve();
  private pending = 0;

  private constructor(path: string) {
    this.path = path;
  }

  /** Create a fresh, empty WAL file (truncating any existing one). */
  static async create(path: string): Promise<Wal> {
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, '', 'utf8');
    return new Wal(path);
  }

  /** Open an existing WAL file and rebuild the in-memory index. */
  static async open(path: string): Promise<Wal> {
    const wal = new Wal(path);
    let content: string;
    try {
      content = await readFile(path, 'utf8');
    } catch (err) {
      if ((err as { code?: string }).code === 'ENOENT') return wal;
      throw err;
    }
    for (const line of content.split('\n')) {
      if (line.trim() === '') continue;
      wal.ingest(parseRecord(line));
    }
    return wal;
  }

  private ingest(record: LogRecord): void {
    this.records.push(record);
    if (record.lsn >= this.nextLSN) this.nextLSN = record.lsn + 1;
    if (record.txnId !== null) this.lastLSNByTxn.set(record.txnId, record.lsn);
  }

  get size(): number {
    return this.records.length;
  }

  /** Number of append operations currently queued or in flight. */
  get pendingWrites(): number {
    return this.pending;
  }

  /** Snapshot of all records in LSN order. */
  scan(): LogRecord[] {
    return [...this.records];
  }

  byLsn(): Map<LSN, LogRecord> {
    return new Map(this.records.map((r) => [r.lsn, r]));
  }

  /**
   * Append a record. Safe to call concurrently: calls are serialized through
   * an internal promise chain, so LSN assignment and prevLSN chaining are
   * atomic with respect to other writers.
   */
  append(draft: LogRecordDraft): Promise<LogRecord> {
    this.pending += 1;
    const result = this.tail.then(() => this.appendLocked(draft));
    this.tail = result.catch(() => undefined);
    return result.finally(() => {
      this.pending -= 1;
    });
  }

  private async appendLocked(draft: LogRecordDraft): Promise<LogRecord> {
    const lsn = this.nextLSN;
    const prevLSN = draft.txnId === null ? null : (this.lastLSNByTxn.get(draft.txnId) ?? null);
    const record = { ...draft, lsn, prevLSN } as LogRecord;
    await appendFile(this.path, serializeRecord(record) + '\n', 'utf8');
    this.ingest(record);
    return record;
  }
}
