import { DiskFullError } from './errors.ts';
import { verifyChecksum } from './checksum.ts';
import { cloneRecord } from './types.ts';
import type { LogRecord, LSN, PageId } from './types.ts';

export interface DiskPage {
  value: number;
  pageLSN: LSN;
}

export interface DiskStats {
  pageWrites: number;
  logAppends: number;
  failedWrites: number;
  /** Records dropped by tail validation (torn write / gap truncation). */
  truncatedRecords: number;
}

/**
 * Simulated durable storage. Survives `DatabaseSystem.crash()` (which only
 * discards volatile state). Supports fault injection: disk-full (ENOSPC) and
 * torn tail writes.
 */
export class SimulatedDisk {
  private pages = new Map<PageId, DiskPage>();
  private wal: LogRecord[] = [];
  private full = false;

  readonly stats: DiskStats = {
    pageWrites: 0,
    logAppends: 0,
    failedWrites: 0,
    truncatedRecords: 0,
  };

  setDiskFull(full: boolean): void {
    this.full = full;
  }

  get isDiskFull(): boolean {
    return this.full;
  }

  writePage(pageId: PageId, value: number, pageLSN: LSN): void {
    if (this.full) {
      this.stats.failedWrites++;
      throw new DiskFullError(`ENOSPC: cannot write page ${pageId}`);
    }
    this.pages.set(pageId, { value, pageLSN });
    this.stats.pageWrites++;
  }

  readPage(pageId: PageId): DiskPage {
    const page = this.pages.get(pageId);
    return page ? { ...page } : { value: 0, pageLSN: 0 };
  }

  pageValue(pageId: PageId): number {
    return this.pages.get(pageId)?.value ?? 0;
  }

  pageLSN(pageId: PageId): LSN {
    return this.pages.get(pageId)?.pageLSN ?? 0;
  }

  appendLog(record: LogRecord): void {
    if (this.full) {
      this.stats.failedWrites++;
      throw new DiskFullError(`ENOSPC: cannot append log record at LSN ${record.lsn}`);
    }
    this.wal.push(cloneRecord(record));
    this.stats.logAppends++;
  }

  /**
   * Reads the durable WAL, validating LSN contiguity and checksums. The log is
   * truncated at the first gap or corrupt record: this is the self-healing
   * behaviour against torn writes caused by power loss or lost log shipments.
   */
  readLog(): LogRecord[] {
    const validLength = this.validPrefixLength();
    this.stats.truncatedRecords += this.wal.length - validLength;
    return this.wal.slice(0, validLength).map(cloneRecord);
  }

  /** Length of the contiguous, checksum-valid prefix of the durable WAL. */
  private validPrefixLength(): number {
    let expectedLSN: LSN = this.wal.length > 0 ? (this.wal[0]?.lsn ?? 1) : 1;
    let count = 0;
    for (const rec of this.wal) {
      if (rec.lsn !== expectedLSN || !verifyChecksum(rec)) break;
      count++;
      expectedLSN = rec.lsn + 1;
    }
    return count;
  }

  /**
   * Physically drops the invalid tail (torn write / gap). Recovery calls this
   * before appending new records so the log stays contiguous. Returns the
   * number of discarded records.
   */
  truncateInvalidTail(): number {
    const validLength = this.validPrefixLength();
    const dropped = this.wal.length - validLength;
    this.wal.length = validLength;
    return dropped;
  }

  /** Unvalidated view of the raw durable log (for tests and diagnostics). */
  rawLog(): LogRecord[] {
    return this.wal.map(cloneRecord);
  }

  /** Fault injection: corrupt the checksum of the last durable record (torn write). */
  corruptTail(): void {
    const tail = this.wal[this.wal.length - 1];
    if (tail) tail.checksum = (tail.checksum + 1) >>> 0;
  }

  /** Fault injection: drop the last `count` durable records (partial flush). */
  truncateTail(count: number): void {
    this.wal.length = Math.max(0, this.wal.length - count);
  }
}
