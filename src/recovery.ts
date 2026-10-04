// ARIES-style recovery: analysis (from the last *complete* checkpoint),
// redo (from the oldest dirty-page recLSN), undo (losers only, CLR-aware).

import { LogManager } from "./logManager.ts";
import { PageStore } from "./pageStore.ts";
import { NULL_LSN } from "./types.ts";
import type { LSN, PageId, TxnId } from "./types.ts";

export interface RecoveryReport {
  startedFromCheckpoint: LSN | null; // beginLSN of the checkpoint used, null = full scan
  checkpointWasTorn: boolean; // a CKPT_BEGIN without its CKPT_END was seen
  tornTailDetected: boolean; // undecodable tail bytes found (e.g. half-written checkpoint)
  redoFromLSN: LSN | null;
  redoneRecords: number;
  loserTxns: TxnId[];
  clrsWritten: LSN[];
}

interface TxnEntry {
  lastLSN: LSN;
  status: "running" | "committed" | "aborting";
}

export function recover(log: LogManager, pages: PageStore): RecoveryReport {
  const report: RecoveryReport = {
    startedFromCheckpoint: null,
    checkpointWasTorn: false,
    tornTailDetected: log.tornTailBytes > 0,
    redoFromLSN: null,
    redoneRecords: 0,
    loserTxns: [],
    clrsWritten: [],
  };

  // ---------- analysis ----------
  const ckpt = log.lastCompleteCheckpoint();
  const txnTable = new Map<TxnId, TxnEntry>();
  const dpt = new Map<PageId, LSN>(); // pageId -> recLSN
  let scanFrom = log.firstAvailableLSN;
  if (ckpt) {
    report.startedFromCheckpoint = ckpt.beginLSN;
    scanFrom = ckpt.beginLSN;
    for (const d of ckpt.end.dirtyPages) dpt.set(d.pageId, d.recLSN);
    for (const t of ckpt.end.activeTxns) {
      txnTable.set(t.txnId, { lastLSN: t.lastLSN, status: t.status });
    }
  }

  const records = log.all();
  const ckptIdsEnded = new Set(
    records.filter((r) => r.type === "CKPT_END").map((r) => (r as { ckptId: number }).ckptId),
  );
  for (const r of records) {
    if (r.type === "CKPT_BEGIN" && !ckptIdsEnded.has(r.ckptId)) {
      report.checkpointWasTorn = true;
    }
  }

  for (const rec of records) {
    if (rec.lsn < scanFrom) continue;
    switch (rec.type) {
      case "BEGIN":
        txnTable.set(rec.txnId, { lastLSN: rec.lsn, status: "running" });
        break;
      case "UPDATE":
      case "CLR":
      case "SAVEPOINT": {
        const t = txnTable.get(rec.txnId) ?? { lastLSN: NULL_LSN, status: "running" as const };
        t.lastLSN = rec.lsn;
        txnTable.set(rec.txnId, t);
        if (rec.type !== "SAVEPOINT" && !dpt.has(rec.pageId)) dpt.set(rec.pageId, rec.lsn);
        break;
      }
      case "COMMIT": {
        const t = txnTable.get(rec.txnId);
        if (t) {
          t.lastLSN = rec.lsn;
          t.status = "committed";
        }
        break;
      }
      case "ABORT": {
        const t = txnTable.get(rec.txnId);
        if (t) {
          t.lastLSN = rec.lsn;
          t.status = "aborting";
        }
        break;
      }
      case "END":
        txnTable.delete(rec.txnId);
        break;
      default:
        break; // checkpoints
    }
  }

  // ---------- redo: repeat history ----------
  let redoFrom: LSN | null = null;
  for (const recLSN of dpt.values()) {
    if (redoFrom === null || recLSN < redoFrom) redoFrom = recLSN;
  }
  report.redoFromLSN = redoFrom;
  if (redoFrom !== null) {
    for (const rec of records) {
      if (rec.lsn < redoFrom) continue;
      if (rec.type === "UPDATE" || rec.type === "CLR") {
        if (pages.redoApply(rec.pageId, rec.after, rec.lsn)) report.redoneRecords++;
      }
    }
  }

  // ---------- undo: losers only, CLR-aware ----------
  for (const [txnId, t] of txnTable) {
    if (t.status === "committed") continue; // committed but END missing: nothing to undo
    report.loserTxns.push(txnId);
    let lsn = t.lastLSN;
    let lastLSN = t.lastLSN;
    while (lsn !== NULL_LSN) {
      const rec = log.read(lsn);
      if (rec.type === "UPDATE") {
        const clrLSN = log.allocLSN();
        log.append({
          type: "CLR",
          lsn: clrLSN,
          txnId,
          pageId: rec.pageId,
          after: rec.before,
          prevLSN: lastLSN,
          undoNextLSN: rec.prevLSN,
          undidLSN: rec.lsn,
        });
        pages.redoApply(rec.pageId, rec.before, clrLSN);
        report.clrsWritten.push(clrLSN);
        lastLSN = clrLSN;
        lsn = rec.prevLSN;
      } else if (rec.type === "CLR") {
        lsn = rec.undoNextLSN; // never re-undo compensated updates
      } else if (rec.type === "BEGIN") {
        break;
      } else if (
        rec.type === "SAVEPOINT" ||
        rec.type === "ABORT" ||
        rec.type === "COMMIT" ||
        rec.type === "END"
      ) {
        lsn = rec.prevLSN;
      } else {
        throw new Error(`unexpected ${rec.type} record in txn chain`);
      }
    }
    if (t.status !== "aborting") {
      const abortLSN = log.allocLSN();
      log.append({ type: "ABORT", lsn: abortLSN, txnId, prevLSN: lastLSN });
      lastLSN = abortLSN;
    }
    const endLSN = log.allocLSN();
    log.append({ type: "END", lsn: endLSN, txnId, prevLSN: lastLSN });
  }

  // Everything still in the pool is now committed state; publish and flush.
  pages.publishAllRecovered();
  pages.flush();
  return report;
}
