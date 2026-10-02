/**
 * Minimal WAL-protocol transaction engine used to build crash scenarios.
 * Follows the write-ahead logging rule: the log record is appended (and the
 * log forced on commit) before the corresponding page may be flushed.
 */

import type { Buffer } from "node:buffer";
import { RecordType, TxnStatus, type CheckpointTxnEntry } from "./logrecord.ts";
import { WalWriter, mapWal, readRecordAt } from "./wal.ts";
import {
  BufferPool,
  PAGE_DATA_OFFSET,
  PageStore,
  setPageLSN,
} from "./pagestore.ts";

interface LiveTxn {
  lastLSN: number;
  status: "running" | "committed" | "aborting";
}

export class MiniDB {
  private nextTxnId = 1;
  private txns = new Map<number, LiveTxn>();
  readonly walPath: string;
  readonly wal: WalWriter;
  readonly store: PageStore;
  readonly pool: BufferPool;

  private constructor(walPath: string, wal: WalWriter, store: PageStore, pool: BufferPool) {
    this.walPath = walPath;
    this.wal = wal;
    this.store = store;
    this.pool = pool;
  }

  static async create(walPath: string, dataPath: string): Promise<MiniDB> {
    const wal = await WalWriter.create(walPath);
    const store = await PageStore.create(dataPath);
    return new MiniDB(walPath, wal, store, new BufferPool(store));
  }

  begin(): number {
    const txnId = this.nextTxnId++;
    this.txns.set(txnId, { lastLSN: 0, status: "running" });
    return txnId;
  }

  private mustTxn(txnId: number): LiveTxn {
    const t = this.txns.get(txnId);
    if (!t) throw new Error(`unknown transaction ${txnId}`);
    return t;
  }

  /** Log an UPDATE, then apply it to the cached page (WAL protocol). */
  async update(txnId: number, pageId: number, offset: number, data: Uint8Array): Promise<number> {
    const t = this.mustTxn(txnId);
    const page = await this.pool.pin(pageId);
    try {
      const before = Buffer.from(
        page.subarray(PAGE_DATA_OFFSET + offset, PAGE_DATA_OFFSET + offset + data.length),
      );
      const lsn = await this.wal.append({
        type: RecordType.UPDATE,
        lsn: 0,
        txnId,
        prevLSN: t.lastLSN,
        pageId,
        offset,
        beforeImage: before,
        afterImage: data,
      });
      page.set(data, PAGE_DATA_OFFSET + offset);
      setPageLSN(page, lsn);
      this.pool.unpin(pageId, { dirty: true, recLSN: lsn });
      t.lastLSN = lsn;
      return lsn;
    } catch (err) {
      this.pool.unpin(pageId);
      throw err;
    }
  }

  /**
   * Commit: append COMMIT and force the log. END is written afterwards and
   * may be lost in a crash (writeEnd: false simulates exactly that).
   */
  async commit(txnId: number, opts: { writeEnd?: boolean } = {}): Promise<number> {
    const t = this.mustTxn(txnId);
    const commitLSN = await this.wal.append({
      type: RecordType.COMMIT,
      lsn: 0,
      txnId,
      prevLSN: t.lastLSN,
    });
    t.lastLSN = commitLSN;
    t.status = "committed";
    await this.wal.flush();
    if (opts.writeEnd ?? true) {
      await this.wal.append({ type: RecordType.END, lsn: 0, txnId, prevLSN: commitLSN });
      this.txns.delete(txnId);
    }
    return commitLSN;
  }

  /**
   * Abort: append ABORT, then roll back updates newest-first writing CLRs.
   * maxClrs limits how many CLRs are written before returning, which
   * simulates a crash in the middle of an abort.
   */
  async abort(txnId: number, maxClrs = Number.POSITIVE_INFINITY): Promise<number> {
    const t = this.mustTxn(txnId);
    const abortLSN = await this.wal.append({
      type: RecordType.ABORT,
      lsn: 0,
      txnId,
      prevLSN: t.lastLSN,
    });
    t.lastLSN = abortLSN;
    t.status = "aborting";
    let lsn = t.lastLSN;
    let written = 0;
    while (lsn !== 0 && written < maxClrs) {
      const record = readRecordAt(mapWal(this.walPath), lsn);
      if (record.type === RecordType.UPDATE) {
        const clrLSN = await this.wal.append({
          type: RecordType.CLR,
          lsn: 0,
          txnId,
          prevLSN: t.lastLSN,
          pageId: record.pageId,
          offset: record.offset,
          beforeImage: record.afterImage,
          afterImage: record.beforeImage,
          undoNextLSN: record.prevLSN,
        });
        const page = await this.pool.pin(record.pageId);
        try {
          page.set(record.beforeImage, PAGE_DATA_OFFSET + record.offset);
          setPageLSN(page, clrLSN);
        } finally {
          this.pool.unpin(record.pageId, { dirty: true, recLSN: clrLSN });
        }
        t.lastLSN = clrLSN;
        written++;
        lsn = record.prevLSN;
      } else if (record.type === RecordType.CLR) {
        lsn = record.undoNextLSN;
      } else {
        lsn = record.prevLSN;
      }
    }
    if (lsn === 0) {
      await this.wal.append({ type: RecordType.END, lsn: 0, txnId, prevLSN: t.lastLSN });
      this.txns.delete(txnId);
    }
    return abortLSN;
  }

  /** Fuzzy checkpoint: BEGIN_CHECKPOINT + END_CHECKPOINT with TT/DPT snapshot. */
  async checkpoint(): Promise<number> {
    const beginLSN = await this.wal.append({
      type: RecordType.BEGIN_CHECKPOINT,
      lsn: 0,
      txnId: 0,
      prevLSN: 0,
    });
    const txns: CheckpointTxnEntry[] = [...this.txns.entries()].map(([txnId, t]) => ({
      txnId,
      status:
        t.status === "committed"
          ? TxnStatus.COMMITTED
          : t.status === "aborting"
            ? TxnStatus.ABORTING
            : TxnStatus.RUNNING,
      lastLSN: t.lastLSN,
    }));
    const pages = this.pool.dirtyPages();
    return this.wal.append({
      type: RecordType.END_CHECKPOINT,
      lsn: 0,
      txnId: 0,
      prevLSN: 0,
      beginLSN,
      txns,
      pages,
    });
  }

  /** Force one buffered page to disk (steal / checkpoint flush). */
  async flushPage(pageId: number): Promise<void> {
    await this.pool.flushPage(pageId);
    await this.store.sync();
  }

  /** Crash: the log is durable, all unflushed page state is lost. */
  async crash(): Promise<void> {
    await this.wal.flush();
    await this.wal.close();
    this.pool.discardAll();
    await this.store.close();
  }
}
