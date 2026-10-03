import { crc32 } from './crc32.ts';
import { DiskFullError, PowerLossError, type FaultInjector } from './faults.ts';
import type { LogRecord, LSN, TxnId } from './types.ts';

/** Durable media: the only state that survives a simulated power loss. */
export interface DurableMedia {
  walLines: string[];
  masterCheckpointLSN: LSN;
}

export function createMedia(): DurableMedia {
  return { walLines: [], masterCheckpointLSN: 0 };
}

export function encodeRecord(rec: LogRecord): string {
  const payload = JSON.stringify(rec);
  return `${crc32(payload).toString(16).padStart(8, '0')} ${payload}`;
}

/**
 * Decodes records until the first corrupt/torn line. A power cut can leave a
 * partially written tail record; its CRC will not match and replay stops
 * there, exactly like a real log scan.
 */
export function decodeRecords(lines: string[]): LogRecord[] {
  const out: LogRecord[] = [];
  for (const line of lines) {
    const sep = line.indexOf(' ');
    if (sep <= 0) break;
    const payload = line.slice(sep + 1);
    const expected = Number.parseInt(line.slice(0, sep), 16);
    if (!Number.isFinite(expected) || crc32(payload) !== expected) break;
    out.push(JSON.parse(payload) as LogRecord);
  }
  return out;
}

/**
 * Write-ahead log. Appends go to a volatile buffer and become durable only
 * via `flush`. Appends are serialized through an internal mutex so concurrent
 * transactions always receive unique, gap-free, monotonically increasing LSNs.
 */
export class WAL {
  private readonly media: DurableMedia;
  private readonly injector: FaultInjector;
  private buffered: LogRecord[] = [];
  private readonly index = new Map<LSN, LogRecord>();
  private nextLSN: LSN = 1;
  private flushedLSN: LSN = 0;
  private maxTxn: TxnId = 0;
  private tail: Promise<void> = Promise.resolve();

  constructor(media: DurableMedia, injector: FaultInjector) {
    this.media = media;
    this.injector = injector;
    const durable = decodeRecords(media.walLines);
    if (durable.length < media.walLines.length) {
      // Truncate the torn tail left by a power cut so new records append cleanly.
      media.walLines.length = durable.length;
    }
    for (const rec of durable) {
      this.index.set(rec.lsn, rec);
      this.nextLSN = rec.lsn + 1;
      if (rec.txnId > this.maxTxn) this.maxTxn = rec.txnId;
    }
    this.flushedLSN = this.nextLSN - 1;
  }

  private enqueue<T>(fn: () => T): Promise<T> {
    const result = this.tail.then(fn);
    this.tail = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  append(rec: Omit<LogRecord, 'lsn'>): Promise<LogRecord> {
    return this.enqueue(() => {
      const full: LogRecord = { ...rec, lsn: this.nextLSN++ };
      this.buffered.push(full);
      this.index.set(full.lsn, full);
      if (full.txnId > this.maxTxn) this.maxTxn = full.txnId;
      return full;
    });
  }

  /** Forces all buffered records up to `uptoLSN` (default: everything) to durable media. */
  flush(uptoLSN?: LSN): Promise<void> {
    const target = uptoLSN ?? this.nextLSN - 1;
    return this.enqueue(() => {
      for (const rec of this.buffered) {
        if (rec.lsn > target) break;
        if (this.injector.check('wal.flush')) {
          throw new DiskFullError(`WAL flush failed at LSN ${rec.lsn}: disk full`);
        }
        const line = encodeRecord(rec);
        if (this.injector.check('wal.powerLoss')) {
          // Torn tail: only a prefix of the record reaches the platter.
          this.media.walLines.push(line.slice(0, Math.max(1, line.length >> 1)));
          throw new PowerLossError(`power loss while flushing LSN ${rec.lsn}`);
        }
        this.media.walLines.push(line);
        this.flushedLSN = rec.lsn;
      }
      this.buffered = this.buffered.filter((rec) => rec.lsn > this.flushedLSN);
    });
  }

  readDurable(): LogRecord[] {
    return decodeRecords(this.media.walLines);
  }

  recordAt(lsn: LSN): LogRecord | undefined {
    return this.index.get(lsn);
  }

  /** Durable + buffered records, in LSN order (test/introspection helper). */
  allRecords(): LogRecord[] {
    return [...this.index.values()].sort((a, b) => a.lsn - b.lsn);
  }

  getFlushedLSN(): LSN {
    return this.flushedLSN;
  }

  getCurrentLSN(): LSN {
    return this.nextLSN - 1;
  }

  bufferedCount(): number {
    return this.buffered.length;
  }

  maxTxnId(): TxnId {
    return this.maxTxn;
  }

  masterLSN(): LSN {
    return this.media.masterCheckpointLSN;
  }

  writeMaster(lsn: LSN): void {
    this.media.masterCheckpointLSN = lsn;
  }
}
