import type { BufferPool } from './bufferpool.ts';
import { CrashError, TornPageError, type FaultInjector } from './faults.ts';
import type { DiskStorage } from './storage.ts';
import type { LogRecord, LSN, Page, PageId, TxnId, TxnTableEntry } from './types.ts';
import { deepCopy } from './util.ts';
import type { WAL } from './wal.ts';

export interface RecoveryReport {
  checkpointLSN: LSN;
  /** Transaction table after analysis. */
  txnTable: Map<TxnId, TxnTableEntry>;
  /** Dirty page table after analysis (pageId -> recLSN). */
  dirtyPageTable: Map<PageId, LSN>;
  losers: TxnId[];
  winners: TxnId[];
  /** LSNs whose updates were re-applied during redo. */
  redone: LSN[];
  /** CLR LSNs written during undo. */
  clrs: LSN[];
  /** Transaction table after undo completes — must be empty (no leaks). */
  txnTableAfter: Map<TxnId, TxnTableEntry>;
}

/**
 * ARIES recovery engine: analysis (rebuild TT/DPT from the last checkpoint),
 * redo (repeat history from the oldest recLSN), undo (roll back losers in
 * reverse LSN order, logging CLRs so recovery is itself idempotent).
 */
export class RecoveryEngine {
  private readonly wal: WAL;
  private readonly disk: DiskStorage;
  private readonly bp: BufferPool;
  private readonly injector: FaultInjector;

  constructor(wal: WAL, disk: DiskStorage, bp: BufferPool, injector: FaultInjector) {
    this.wal = wal;
    this.disk = disk;
    this.bp = bp;
    this.injector = injector;
  }

  async recover(): Promise<RecoveryReport> {
    const records = this.wal.readDurable();
    const { tt, dpt, checkpointLSN } = this.analysis(records);
    // Snapshot analysis results before redo/undo mutate the tables.
    const txnTableSnapshot = new Map([...tt.entries()].map(([id, e]) => [id, { ...e }]));
    const dptSnapshot = new Map(dpt);
    const redone = this.redo(records, dpt);
    const { clrs, losers, winners } = await this.undo(tt);
    await this.wal.flush();
    await this.bp.flushAll();
    return {
      checkpointLSN,
      txnTable: txnTableSnapshot,
      dirtyPageTable: dptSnapshot,
      losers,
      winners,
      redone,
      clrs,
      txnTableAfter: tt, // undo removes every entry as it ENDs the txn
    };
  }

  // ------------------------------------------------------------- analysis
  private analysis(records: LogRecord[]): {
    tt: Map<TxnId, TxnTableEntry>;
    dpt: Map<PageId, LSN>;
    checkpointLSN: LSN;
  } {
    const tt = new Map<TxnId, TxnTableEntry>();
    const dpt = new Map<PageId, LSN>();
    let start = 0;
    const checkpointLSN = this.wal.masterLSN();
    if (checkpointLSN > 0) {
      const idx = records.findIndex((r) => r.lsn === checkpointLSN && r.type === 'CHECKPOINT');
      if (idx >= 0) {
        const ckpt = records[idx];
        for (const entry of Object.values(ckpt.tt ?? {})) {
          tt.set(entry.txnId, { ...entry });
        }
        for (const [pid, lsn] of Object.entries(ckpt.dpt ?? {})) {
          dpt.set(Number(pid), lsn);
        }
        start = idx + 1;
      }
    }
    for (let i = start; i < records.length; i++) {
      const rec = records[i];
      if (rec.type === 'CHECKPOINT') continue;
      if (rec.type === 'END') {
        tt.delete(rec.txnId);
        continue;
      }
      let txn = tt.get(rec.txnId);
      if (!txn) {
        txn = { txnId: rec.txnId, status: 'RUNNING', lastLSN: rec.lsn };
        tt.set(rec.txnId, txn);
      }
      txn.lastLSN = rec.lsn;
      if (rec.type === 'COMMIT') txn.status = 'COMMITTED';
      else if (rec.type === 'ABORT') txn.status = 'ABORTING';
      else if (
        (rec.type === 'UPDATE' || rec.type === 'CLR') &&
        rec.pageId !== undefined &&
        !dpt.has(rec.pageId)
      ) {
        dpt.set(rec.pageId, rec.lsn);
      }
    }
    return { tt, dpt, checkpointLSN };
  }

  // ----------------------------------------------------------------- redo
  private redo(records: LogRecord[], dpt: Map<PageId, LSN>): LSN[] {
    // A torn page has an untrustworthy pageLSN: replay its full history.
    for (const pageId of [...dpt.keys()]) {
      try {
        this.disk.readPage(pageId);
      } catch (err) {
        if (err instanceof TornPageError) dpt.set(pageId, 0);
        else throw err;
      }
    }
    const redone: LSN[] = [];
    if (dpt.size === 0) return redone;
    const redoFrom = Math.min(...dpt.values());
    for (const rec of records) {
      if (rec.lsn < redoFrom) continue;
      if (rec.type !== 'UPDATE' && rec.type !== 'CLR') continue;
      const pageId = rec.pageId!;
      const recLSN = dpt.get(pageId);
      if (recLSN === undefined || recLSN > rec.lsn) continue;
      let page: Page;
      try {
        // Fetch through the buffer pool so successive records for the same
        // page build on the frame reconstructed by earlier redo steps.
        page = this.bp.fetchPage(pageId);
      } catch (err) {
        if (err instanceof TornPageError) page = { pageId, pageLSN: 0, data: {} };
        else throw err;
      }
      if (page.pageLSN >= rec.lsn) continue; // already on disk
      if (rec.after === null || rec.after === undefined) delete page.data[rec.key!];
      else page.data[rec.key!] = deepCopy(rec.after);
      page.pageLSN = rec.lsn;
      this.bp.installDirty(page, rec.lsn);
      redone.push(rec.lsn);
    }
    return redone;
  }

  // ----------------------------------------------------------------- undo
  private async undo(tt: Map<TxnId, TxnTableEntry>): Promise<{
    clrs: LSN[];
    losers: TxnId[];
    winners: TxnId[];
  }> {
    const byLSN = new Map<LSN, LogRecord>();
    for (const rec of this.wal.readDurable()) byLSN.set(rec.lsn, rec);

    const winners = [...tt.values()]
      .filter((t) => t.status === 'COMMITTED')
      .map((t) => t.txnId)
      .sort((a, b) => a - b);
    const losers = [...tt.values()]
      .filter((t) => t.status !== 'COMMITTED')
      .map((t) => t.txnId)
      .sort((a, b) => a - b);

    // Winners whose END record was lost get it rewritten.
    for (const txnId of winners) {
      const txn = tt.get(txnId)!;
      const end = await this.wal.append({ type: 'END', txnId, prevLSN: txn.lastLSN });
      byLSN.set(end.lsn, end);
      tt.delete(txnId);
    }

    // Reverse scan: always undo the largest outstanding LSN across losers.
    const pending = new Map<TxnId, LSN>();
    for (const txnId of losers) pending.set(txnId, tt.get(txnId)!.lastLSN);
    const clrs: LSN[] = [];

    while (pending.size > 0) {
      if (this.injector.check('recovery.undo')) {
        throw new CrashError('simulated power loss during undo');
      }
      let victimTxn = -1;
      let victimLSN = -1;
      for (const [txnId, lsn] of pending) {
        if (lsn > victimLSN || (lsn === victimLSN && txnId > victimTxn)) {
          victimLSN = lsn;
          victimTxn = txnId;
        }
      }
      const rec = byLSN.get(victimLSN);
      if (!rec) throw new Error(`broken undo chain at LSN ${victimLSN}`);

      let next: LSN;
      if (rec.type === 'UPDATE') {
        const clr = await this.wal.append({
          type: 'CLR',
          txnId: victimTxn,
          prevLSN: victimLSN,
          pageId: rec.pageId,
          key: rec.key,
          before: rec.after,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        byLSN.set(clr.lsn, clr);
        clrs.push(clr.lsn);
        const page = this.bp.fetchPage(rec.pageId!);
        if (rec.before === null || rec.before === undefined) delete page.data[rec.key!];
        else page.data[rec.key!] = deepCopy(rec.before);
        page.pageLSN = clr.lsn;
        this.bp.markDirty(rec.pageId!, clr.lsn);
        next = rec.prevLSN;
      } else if (rec.type === 'CLR') {
        // Already undone before the crash: skip via undoNextLSN (idempotent).
        next = rec.undoNextLSN ?? 0;
      } else if (rec.type === 'BEGIN') {
        next = 0;
      } else {
        next = rec.prevLSN;
      }

      if (next === 0) {
        const end = await this.wal.append({ type: 'END', txnId: victimTxn, prevLSN: victimLSN });
        byLSN.set(end.lsn, end);
        pending.delete(victimTxn);
        tt.delete(victimTxn);
      } else {
        pending.set(victimTxn, next);
      }
    }
    return { clrs, losers, winners };
  }
}
