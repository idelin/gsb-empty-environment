import { WalWriter } from './wal.ts';
import type { BufferedPage, DiskPages, Page } from './storage.ts';
import type { DirtyPageEntry, LogRecord, TxnStatus, TxnTableEntry } from './types.ts';

export class Database {
  private readonly disk: DiskPages = new Map();
  private readonly buffer = new Map<string, BufferedPage>();
  private readonly wal: WalWriter;
  private readonly walPath: string;
  private readonly txnStatus = new Map<number, TxnStatus>();
  private readonly txnLastLSN = new Map<number, number>();
  private readonly dirtyPages = new Map<string, number>();
  private nextTxId = 1;
  private crashed = false;

  private constructor(walPath: string, wal: WalWriter) {
    this.walPath = walPath;
    this.wal = wal;
  }

  static create(walPath: string): Database {
    return new Database(walPath, WalWriter.create(walPath));
  }

  seedPage(pageId: string, value: number): void {
    this.disk.set(pageId, { data: value, pageLSN: 0 });
  }

  begin(): number {
    this.assertRunning();
    const txId = this.nextTxId++;
    const record = this.wal.append({ type: 'BEGIN', txId, prevLSN: 0 });
    this.txnStatus.set(txId, 'RUNNING');
    this.txnLastLSN.set(txId, record.lsn);
    return txId;
  }

  write(txId: number, pageId: string, value: number): void {
    this.assertRunning();
    if (this.txnStatus.get(txId) !== 'RUNNING') {
      throw new Error(`transaction ${txId} is not running`);
    }
    const page = this.fetchPage(pageId);
    const prevLSN = this.txnLastLSN.get(txId) ?? 0;
    const record = this.wal.append({
      type: 'UPDATE',
      txId,
      prevLSN,
      pageId,
      before: page.data,
      after: value,
    });
    page.data = value;
    page.pageLSN = record.lsn;
    page.dirty = true;
    if (!this.dirtyPages.has(pageId)) this.dirtyPages.set(pageId, record.lsn);
    this.txnLastLSN.set(txId, record.lsn);
  }

  commit(txId: number): void {
    this.assertRunning();
    if (this.txnStatus.get(txId) !== 'RUNNING') {
      throw new Error(`transaction ${txId} is not running`);
    }
    const prevLSN = this.txnLastLSN.get(txId) ?? 0;
    const commit = this.wal.append({ type: 'COMMIT', txId, prevLSN });
    this.wal.flush();
    this.txnStatus.set(txId, 'COMMITTED');
    this.wal.append({ type: 'END', txId, prevLSN: commit.lsn });
    this.txnStatus.delete(txId);
    this.txnLastLSN.delete(txId);
  }

  abort(txId: number): void {
    this.assertRunning();
    if (this.txnStatus.get(txId) !== 'RUNNING') {
      throw new Error(`transaction ${txId} is not running`);
    }
    const prevLSN = this.txnLastLSN.get(txId) ?? 0;
    this.wal.append({ type: 'ABORT', txId, prevLSN });
    this.txnStatus.set(txId, 'ABORTING');
    let lsn = prevLSN;
    let chainPrev = this.wal.currentLSN - 1;
    while (lsn > 0) {
      const record = this.wal.recordAt(lsn);
      if (record.type === 'UPDATE') {
        const page = this.fetchPage(record.pageId);
        const clr = this.wal.append({
          type: 'CLR',
          txId,
          prevLSN: chainPrev,
          pageId: record.pageId,
          after: record.before,
          undoNextLSN: record.prevLSN,
        });
        chainPrev = clr.lsn;
        page.data = record.before;
        page.pageLSN = clr.lsn;
        page.dirty = true;
        if (!this.dirtyPages.has(record.pageId)) this.dirtyPages.set(record.pageId, clr.lsn);
        lsn = record.prevLSN;
      } else if (record.type === 'CLR') {
        lsn = record.undoNextLSN;
      } else if (record.type === 'BEGIN') {
        this.wal.append({ type: 'END', txId, prevLSN: chainPrev });
        lsn = 0;
      } else {
        lsn = record.prevLSN;
      }
    }
    this.txnStatus.delete(txId);
    this.txnLastLSN.delete(txId);
  }

  checkpoint(): number {
    this.assertRunning();
    const txTable: TxnTableEntry[] = [...this.txnStatus.entries()].map(([txId, status]) => ({
      txId,
      status,
      lastLSN: this.txnLastLSN.get(txId) ?? 0,
    }));
    const dirtyPages: DirtyPageEntry[] = [...this.dirtyPages.entries()].map(([pageId, recLSN]) => ({
      pageId,
      recLSN,
    }));
    const record = this.wal.append({ type: 'CHECKPOINT', txTable, dirtyPages });
    this.wal.flush();
    return record.lsn;
  }

  flushPage(pageId: string): void {
    const page = this.buffer.get(pageId);
    if (!page || !page.dirty) return;
    this.disk.set(pageId, { data: page.data, pageLSN: page.pageLSN });
    page.dirty = false;
    this.dirtyPages.delete(pageId);
  }

  flushAll(): void {
    for (const pageId of [...this.buffer.keys()]) this.flushPage(pageId);
  }

  crash(): void {
    this.wal.flush();
    this.buffer.clear();
    this.dirtyPages.clear();
    this.txnStatus.clear();
    this.txnLastLSN.clear();
    this.crashed = true;
  }

  close(): void {
    this.wal.close();
  }

  diskValue(pageId: string): number | undefined {
    return this.disk.get(pageId)?.data;
  }

  get diskPages(): DiskPages {
    return this.disk;
  }

  get walRecords(): readonly LogRecord[] {
    return this.wal.records;
  }

  get walFilePath(): string {
    return this.walPath;
  }

  get walClosed(): boolean {
    return this.wal.closed;
  }

  private fetchPage(pageId: string): BufferedPage {
    const cached = this.buffer.get(pageId);
    if (cached) return cached;
    const onDisk: Page | undefined = this.disk.get(pageId);
    const page: BufferedPage = onDisk
      ? { data: onDisk.data, pageLSN: onDisk.pageLSN, dirty: false }
      : { data: 0, pageLSN: 0, dirty: false };
    this.buffer.set(pageId, page);
    return page;
  }

  private assertRunning(): void {
    if (this.crashed) throw new Error('database has crashed; run recovery before new work');
  }
}
