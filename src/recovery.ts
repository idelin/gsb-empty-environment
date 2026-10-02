/**
 * ARIES crash recovery: Analysis, Redo (repeat history), Undo (with CLRs).
 *
 * - Analysis locates the most recent END_CHECKPOINT, seeds the transaction
 *   table (TT) and dirty page table (DPT) from its snapshot, then scans
 *   forward to end-of-log maintaining both tables.
 * - Redo scans from the smallest recLSN in the DPT and repeats history for
 *   UPDATE and CLR records whose page is in the DPT, guarded by pageLSN.
 * - Undo walks loser transactions backwards in descending LSN order, writing
 *   a compensation log record (CLR) per undone UPDATE and an END record when
 *   a transaction is fully rolled back. CLRs carry undoNextLSN so a crash
 *   during undo never undoes the same update twice.
 */

import type { Buffer } from "node:buffer";
import { RecordType, TxnStatus, type EndCheckpointRecord, type LogRecord } from "./logrecord.ts";
import {
  WAL_HEADER_SIZE,
  WalWriter,
  mapWal,
  readRecordAt,
  scanWal,
} from "./wal.ts";
import {
  BufferPool,
  PAGE_DATA_OFFSET,
  PageStore,
  getPageLSN,
  setPageLSN,
} from "./pagestore.ts";

export type TxnState = "running" | "committed" | "aborting";

export interface TxnTableEntry {
  txnId: number;
  status: TxnState;
  lastLSN: number;
}

export interface AnalysisResult {
  txnTable: Map<number, TxnTableEntry>;
  /** pageId -> recLSN (oldest LSN that may have dirtied the page). */
  dpt: Map<number, number>;
  /** Smallest recLSN; 0 when the DPT is empty. */
  redoLSN: number;
  /** LSN of the END_CHECKPOINT the scan started from; 0 when none. */
  checkpointLSN: number;
  /** First offset past the last complete record. */
  endOfLog: number;
}

function statusFromCode(code: number): TxnState {
  switch (code) {
    case TxnStatus.COMMITTED:
      return "committed";
    case TxnStatus.ABORTING:
      return "aborting";
    default:
      return "running";
  }
}

export function analysis(mapped: Buffer): AnalysisResult {
  // Pass 1: find the last END_CHECKPOINT and the logical end of log.
  let checkpoint: { lsn: number; record: EndCheckpointRecord } | null = null;
  let endOfLog = WAL_HEADER_SIZE;
  for (const e of scanWal(mapped)) {
    endOfLog = e.nextOffset;
    if (e.record.type === RecordType.END_CHECKPOINT) {
      checkpoint = { lsn: e.lsn, record: e.record };
    }
  }

  const txnTable = new Map<number, TxnTableEntry>();
  const dpt = new Map<number, number>();
  if (checkpoint) {
    for (const t of checkpoint.record.txns) {
      txnTable.set(t.txnId, { txnId: t.txnId, status: statusFromCode(t.status), lastLSN: t.lastLSN });
    }
    for (const p of checkpoint.record.pages) dpt.set(p.pageId, p.recLSN);
  }

  // Pass 2: scan forward from the checkpoint, maintaining TT and DPT.
  const from = checkpoint ? checkpoint.lsn : WAL_HEADER_SIZE;
  const ensure = (txnId: number): TxnTableEntry => {
    let t = txnTable.get(txnId);
    if (!t) {
      t = { txnId, status: "running", lastLSN: 0 };
      txnTable.set(txnId, t);
    }
    return t;
  };
  for (const { lsn, record } of scanWal(mapped, { from })) {
    switch (record.type) {
      case RecordType.UPDATE:
      case RecordType.CLR: {
        ensure(record.txnId).lastLSN = lsn;
        if (!dpt.has(record.pageId)) dpt.set(record.pageId, lsn);
        break;
      }
      case RecordType.COMMIT: {
        const t = ensure(record.txnId);
        t.status = "committed";
        t.lastLSN = lsn;
        break;
      }
      case RecordType.ABORT: {
        const t = ensure(record.txnId);
        t.status = "aborting";
        t.lastLSN = lsn;
        break;
      }
      case RecordType.END:
        txnTable.delete(record.txnId);
        break;
      default:
        break;
    }
  }

  let redoLSN = 0;
  for (const recLSN of dpt.values()) {
    redoLSN = redoLSN === 0 ? recLSN : Math.min(redoLSN, recLSN);
  }
  return { txnTable, dpt, redoLSN, checkpointLSN: checkpoint?.lsn ?? 0, endOfLog };
}

export interface RedoResult {
  /** LSNs actually re-applied to pages. */
  redone: number[];
  /** UPDATE/CLR LSNs skipped (page already current or not in DPT). */
  skipped: number[];
}

export async function redo(
  mapped: Buffer,
  a: AnalysisResult,
  pool: BufferPool,
): Promise<RedoResult> {
  const redone: number[] = [];
  const skipped: number[] = [];
  if (a.dpt.size === 0) return { redone, skipped };
  for (const { lsn, record } of scanWal(mapped, { from: a.redoLSN })) {
    if (record.type !== RecordType.UPDATE && record.type !== RecordType.CLR) continue;
    const recLSN = a.dpt.get(record.pageId);
    if (recLSN === undefined || recLSN > lsn) {
      skipped.push(lsn);
      continue;
    }
    const page = await pool.pin(record.pageId);
    let applied = false;
    try {
      if (getPageLSN(page) < lsn) {
        page.set(record.afterImage, PAGE_DATA_OFFSET + record.offset);
        setPageLSN(page, lsn);
        applied = true;
        redone.push(lsn);
      } else {
        skipped.push(lsn);
      }
    } finally {
      pool.unpin(record.pageId, applied ? { dirty: true, recLSN: lsn } : {});
    }
  }
  return { redone, skipped };
}

export interface UndoResult {
  /** LSNs of CLRs written during undo. */
  clrs: number[];
  /** LSNs of END records written for fully rolled-back transactions. */
  ends: number[];
  /** Transactions rolled back, in completion order. */
  undoneTxns: number[];
}

export async function undo(
  mapped: Buffer,
  a: AnalysisResult,
  pool: BufferPool,
  wal: WalWriter,
): Promise<UndoResult> {
  const losers = new Map<number, number>();
  for (const [txnId, t] of a.txnTable) {
    if (t.status !== "committed") losers.set(txnId, t.lastLSN);
  }
  const clrs: number[] = [];
  const ends: number[] = [];
  const undoneTxns: number[] = [];

  while (losers.size > 0) {
    // Always undo the largest outstanding LSN across all losers.
    let maxTxn = -1;
    let maxLSN = -1;
    for (const [txnId, lsn] of losers) {
      if (lsn > maxLSN) {
        maxLSN = lsn;
        maxTxn = txnId;
      }
    }
    const record = readRecordAt(mapped, maxLSN);
    if (record.type === RecordType.UPDATE) {
      const clrLSN = await wal.append({
        type: RecordType.CLR,
        lsn: 0,
        txnId: record.txnId,
        prevLSN: maxLSN,
        pageId: record.pageId,
        offset: record.offset,
        beforeImage: record.afterImage,
        afterImage: record.beforeImage,
        undoNextLSN: record.prevLSN,
      });
      clrs.push(clrLSN);
      const page = await pool.pin(record.pageId);
      try {
        page.set(record.beforeImage, PAGE_DATA_OFFSET + record.offset);
        setPageLSN(page, clrLSN);
      } finally {
        pool.unpin(record.pageId, { dirty: true, recLSN: clrLSN });
      }
      if (record.prevLSN === 0) {
        ends.push(await writeEnd(wal, record.txnId, clrLSN));
        losers.delete(record.txnId);
        undoneTxns.push(record.txnId);
      } else {
        losers.set(record.txnId, record.prevLSN);
      }
    } else if (record.type === RecordType.CLR) {
      // Already compensated (crash during a previous undo): skip via undoNextLSN.
      if (record.undoNextLSN === 0) {
        ends.push(await writeEnd(wal, record.txnId, maxLSN));
        losers.delete(record.txnId);
        undoneTxns.push(record.txnId);
      } else {
        losers.set(record.txnId, record.undoNextLSN);
      }
    } else {
      // ABORT (or any non-update) record: keep walking the chain.
      if (record.prevLSN === 0) {
        ends.push(await writeEnd(wal, record.txnId, maxLSN));
        losers.delete(record.txnId);
        undoneTxns.push(record.txnId);
      } else {
        losers.set(record.txnId, record.prevLSN);
      }
    }
  }
  return { clrs, ends, undoneTxns };
}

async function writeEnd(wal: WalWriter, txnId: number, prevLSN: number): Promise<number> {
  return wal.append({ type: RecordType.END, lsn: 0, txnId, prevLSN });
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redo: RedoResult;
  undo: UndoResult;
}

/** Full ARIES restart recovery: analysis -> redo -> undo, then force pages. */
export async function recover(walPath: string, store: PageStore): Promise<RecoveryReport> {
  const mapped = mapWal(walPath);
  const a = analysis(mapped);
  const pool = new BufferPool(store);
  const redoResult = await redo(mapped, a, pool);
  const wal = await WalWriter.openForRecovery(walPath);
  const undoResult = await undo(mapped, a, pool, wal);
  await wal.flush();
  await wal.close();
  await pool.flushAll();
  pool.assertNoLeaks();
  return { analysis: a, redo: redoResult, undo: undoResult };
}

export type { LogRecord };
