import { computeChecksum } from './checksum.ts';
import { NULL_LSN, cloneRecord } from './types.ts';
import type { LogRecord, LogRecordPayload, LSN } from './types.ts';
import type { SimulatedDisk } from './disk.ts';

export type AppendHook = (record: LogRecord) => void;

/**
 * WAL manager. Owns the volatile log tail buffer and forces it to durable
 * storage on demand. Implements the WAL protocol primitives used by the
 * buffer pool (flush log up to pageLSN before stealing a page) and by
 * transactions (force log up to the commit record at commit time).
 */
export class LogManager {
  private buffer: LogRecord[] = [];
  private nextLSN: LSN = 1;
  private appendHook: AppendHook | null = null;
  private readonly disk: SimulatedDisk;

  constructor(disk: SimulatedDisk) {
    this.disk = disk;
    const durable = disk.readLog();
    const last = durable[durable.length - 1];
    this.nextLSN = last ? last.lsn + 1 : 1;
    // Self-healing: physically drop any torn/corrupt tail before new appends.
    this.disk.truncateInvalidTail();
  }

  /** Registers a fault-injection hook fired after every append. */
  setAppendHook(hook: AppendHook | null): void {
    this.appendHook = hook;
  }

  append(payload: LogRecordPayload): LSN {
    const lsn = this.nextLSN++;
    const record = { ...payload, lsn, checksum: 0 } as LogRecord;
    record.checksum = computeChecksum(record);
    this.buffer.push(record);
    this.appendHook?.(record);
    return lsn;
  }

  /**
   * Forces every buffered record with lsn <= uptoLSN to disk, in order.
   * On ENOSPC the unwritten tail stays buffered so the flush can simply be
   * retried once space is available (no partial/corrupt state).
   */
  flush(uptoLSN: LSN): void {
    while (this.buffer.length > 0) {
      const head = this.buffer[0];
      if (!head || head.lsn > uptoLSN) break;
      this.disk.appendLog(head); // may throw DiskFullError; head stays buffered
      this.buffer.shift();
    }
  }

  flushAll(): void {
    const last = this.buffer[this.buffer.length - 1];
    if (last) this.flush(last.lsn);
  }

  /** LSN of the last record known to be durable. */
  getDurableLSN(): LSN {
    const durable = this.disk.readLog();
    const last = durable[durable.length - 1];
    return last ? last.lsn : NULL_LSN;
  }

  /** LSN of the most recently appended record (durable or not). */
  getCurrentLSN(): LSN {
    return this.nextLSN - 1;
  }

  get bufferedCount(): number {
    return this.buffer.length;
  }

  /** All records, durable prefix followed by the volatile tail. */
  readAll(): LogRecord[] {
    return [...this.disk.readLog(), ...this.buffer.map(cloneRecord)];
  }

  /** Power loss: the volatile log tail vanishes. */
  crash(): void {
    this.buffer = [];
    this.appendHook = null;
  }
}
