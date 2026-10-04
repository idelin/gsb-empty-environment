import { LogManager } from "./log.ts";
import { PageStore } from "./pagestore.ts";
import type { LSN, PageId, TxnId, TxnTableEntry } from "./types.ts";

export interface RecoveryReport {
  analysis: {
    checkpointUsed: LSN | null;
    scanFrom: LSN;
    winners: TxnId[];
    losers: TxnId[];
  };
  redo: { startLSN: LSN; applied: number };
  undo: { clrsWritten: LSN[]; endedTxns: TxnId[] };
}

/** ARIES-style restart recovery: analysis -> redo (repeat history) -> undo (losers, CLR-aware). */
export function recover(log: LogManager, pages: PageStore): RecoveryReport {
  // ---------- analysis ----------
  const chkpt = log.lastCompleteCheckpoint();
  const txnTable = new Map<TxnId, TxnTableEntry>();
  const dpt = new Map<PageId, LSN>();
  let scanFrom: LSN;
  if (chkpt) {
    scanFrom = chkpt.beginLSN;
    for (const t of chkpt.txns) txnTable.set(t.txn, { ...t });
    for (const d of chkpt.dirtyPages) dpt.set(d.page, d.recLSN);
  } else {
    scanFrom = log.records.length ? log.records[0].lsn : 1;
  }
  for (const rec of log.records) {
    if (rec.lsn < scanFrom) continue;
    switch (rec.type) {
      case "update":
      case "clr": {
        let e = txnTable.get(rec.txn);
        if (!e) {
          e = { txn: rec.txn, status: "active", lastLSN: rec.lsn, firstLSN: rec.lsn };
          txnTable.set(rec.txn, e);
        }
        e.lastLSN = rec.lsn;
        if (!dpt.has(rec.page)) dpt.set(rec.page, rec.lsn);
        break;
      }
      case "commit": {
        const e = txnTable.get(rec.txn);
        if (e) {
          e.status = "committed";
          e.lastLSN = rec.lsn;
        }
        break;
      }
      case "abort": {
        const e = txnTable.get(rec.txn);
        if (e) {
          e.status = "aborting";
          e.lastLSN = rec.lsn;
        }
        break;
      }
      case "end":
        txnTable.delete(rec.txn);
        break;
    }
  }
  const winners = [...txnTable.values()].filter((e) => e.status === "committed").map((e) => e.txn);
  const losers = [...txnTable.values()].filter((e) => e.status !== "committed").map((e) => e.txn);

  // ---------- redo ----------
  let redoStart = Infinity;
  for (const r of dpt.values()) redoStart = Math.min(redoStart, r);
  if (redoStart === Infinity) redoStart = log.nextLSN;
  let applied = 0;
  for (const rec of log.records) {
    if (rec.lsn < redoStart) continue;
    if (rec.type !== "update" && rec.type !== "clr") continue;
    const recLSN = dpt.get(rec.page);
    if (recLSN === undefined || recLSN > rec.lsn) continue;
    if (pages.pageLSN(rec.page) >= rec.lsn) continue;
    pages.applyUpdate(rec.page, rec.after, rec.lsn, null);
    applied++;
  }

  // ---------- undo ----------
  const pending = new Map<TxnId, LSN>();
  const lastLSN = new Map<TxnId, LSN>();
  for (const e of txnTable.values()) {
    if (e.status !== "committed") {
      pending.set(e.txn, e.lastLSN);
      lastLSN.set(e.txn, e.lastLSN);
    }
  }
  const clrsWritten: LSN[] = [];
  const endedTxns: TxnId[] = [];
  for (;;) {
    let pick: TxnId | null = null;
    let pickLSN = 0;
    for (const [t, cur] of pending) {
      if (cur > pickLSN) {
        pick = t;
        pickLSN = cur;
      }
    }
    if (pick === null) break;
    const rec = log.get(pickLSN);
    if (!rec) {
      throw new Error(`recovery: log record ${pickLSN} needed to undo txn ${pick} is missing (log truncated too far?)`);
    }
    if (rec.type === "update") {
      const clr = log.append({
        type: "clr",
        txn: pick,
        page: rec.page,
        before: rec.after,
        after: rec.before,
        prevLSN: lastLSN.get(pick)!,
        undoNext: rec.prevLSN,
      });
      pages.applyUpdate(rec.page, rec.before, clr.lsn, null);
      clrsWritten.push(clr.lsn);
      lastLSN.set(pick, clr.lsn);
      pending.set(pick, rec.prevLSN);
    } else if (rec.type === "clr") {
      pending.set(pick, rec.undoNext); // never undo a CLR
    } else if (rec.type === "abort" || rec.type === "commit") {
      pending.set(pick, rec.prevLSN);
    } else {
      pending.set(pick, 0);
    }
    if (pending.get(pick) === 0) {
      const e = log.append({ type: "end", txn: pick, prevLSN: lastLSN.get(pick)! });
      lastLSN.set(pick, e.lsn);
      pending.delete(pick);
      endedTxns.push(pick);
    }
  }

  pages.finalizeRecovery();
  return {
    analysis: { checkpointUsed: chkpt ? chkpt.lsn : null, scanFrom, winners, losers },
    redo: { startLSN: redoStart === log.nextLSN && dpt.size === 0 ? log.nextLSN : redoStart, applied },
    undo: { clrsWritten, endedTxns },
  };
}
