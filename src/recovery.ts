import type { Database } from "./database.ts";
import type { LSN, TxnId, TxnTableEntry } from "./types.ts";
import { NULL_LSN } from "./types.ts";

export interface RecoveryReport {
  analysis: {
    winners: TxnId[];
    losers: TxnId[];
    dirtyPages: number;
  };
  redo: {
    startLSN: LSN;
    applied: number;
    skipped: number;
  };
  undo: {
    txnIds: TxnId[];
    clrCount: number;
  };
}

export function recover(db: Database): RecoveryReport {
  const { losers, winners } = analyze(db);
  const loserIds = [...losers.keys()];
  const redoReport = redo(db);
  const undoReport = undo(db, losers);
  db.log.flush();
  db.buffer.flushAll();
  return {
    analysis: {
      winners,
      losers: loserIds,
      dirtyPages: db.dpt.size,
    },
    redo: redoReport,
    undo: undoReport,
  };
}

function analyze(db: Database): {
  losers: Map<TxnId, TxnTableEntry>;
  winners: TxnId[];
} {
  const checkpoint = db.log.lastCheckpoint();
  let startLSN = 1;
  if (checkpoint) {
    for (const entry of checkpoint.txnTable) {
      db.txnTable.set(entry.txnId, { ...entry });
    }
    for (const [pageId, recLSN] of checkpoint.dpt) {
      db.dpt.set(pageId, recLSN);
    }
    startLSN = checkpoint.lsn + 1;
  }

  let maxTxnId = 0;
  for (const rec of db.log.scan(startLSN)) {
    if (rec.type === "checkpoint") continue;
    maxTxnId = Math.max(maxTxnId, rec.txnId);
    switch (rec.type) {
      case "begin":
        db.txnTable.set(rec.txnId, {
          txnId: rec.txnId,
          status: "running",
          lastLSN: rec.lsn,
        });
        break;
      case "update":
      case "clr": {
        const txn = db.txnTable.get(rec.txnId);
        if (txn) {
          txn.lastLSN = rec.lsn;
        } else {
          db.txnTable.set(rec.txnId, {
            txnId: rec.txnId,
            status: "running",
            lastLSN: rec.lsn,
          });
        }
        if (!db.dpt.has(rec.pageId)) db.dpt.set(rec.pageId, rec.lsn);
        break;
      }
      case "commit":
      case "abort": {
        const txn = db.txnTable.get(rec.txnId);
        if (txn) {
          txn.status = rec.type === "commit" ? "committed" : "aborted";
          txn.lastLSN = rec.lsn;
        }
        break;
      }
      case "end":
        db.txnTable.delete(rec.txnId);
        break;
    }
  }
  for (const entry of checkpoint?.txnTable ?? []) {
    maxTxnId = Math.max(maxTxnId, entry.txnId);
  }
  db.restoreTxnCounter(maxTxnId + 1);

  const losers = new Map<TxnId, TxnTableEntry>();
  const winners: TxnId[] = [];
  for (const [txnId, entry] of db.txnTable) {
    if (entry.status === "committed") {
      winners.push(txnId);
      db.log.append({ type: "end", txnId, prevLSN: entry.lastLSN });
      db.txnTable.delete(txnId);
    } else {
      losers.set(txnId, entry);
    }
  }
  return { losers, winners };
}

function redo(db: Database): RecoveryReport["redo"] {
  let startLSN = db.log.lastLSN + 1;
  for (const recLSN of db.dpt.values()) {
    startLSN = Math.min(startLSN, recLSN);
  }
  let applied = 0;
  let skipped = 0;
  for (const rec of db.log.scan(startLSN)) {
    if (rec.type !== "update" && rec.type !== "clr") continue;
    const recLSN = db.dpt.get(rec.pageId);
    if (recLSN === undefined || recLSN > rec.lsn) {
      skipped++;
      continue;
    }
    const frame = db.buffer.fetchPage(rec.pageId);
    if (frame.pageLSN >= rec.lsn) {
      skipped++;
      continue;
    }
    Buffer.from(rec.after, "hex").copy(frame.data, rec.offset);
    db.markDirty(frame, rec.lsn);
    applied++;
  }
  return { startLSN, applied, skipped };
}

function undo(
  db: Database,
  losers: Map<TxnId, TxnTableEntry>,
): RecoveryReport["undo"] {
  let clrCount = 0;
  const undoneTxnIds: TxnId[] = [];
  while (losers.size > 0) {
    let current: TxnTableEntry | null = null;
    for (const entry of losers.values()) {
      if (!current || entry.lastLSN > current.lastLSN) current = entry;
    }
    if (!current) break;
    const rec = db.log.getRecord(current.lastLSN);
    if (rec.type === "update") {
      const clrLSN = db.log.append({
        type: "clr",
        txnId: current.txnId,
        prevLSN: current.lastLSN,
        undoNextLSN: rec.prevLSN,
        pageId: rec.pageId,
        offset: rec.offset,
        before: rec.after,
        after: rec.before,
      });
      const frame = db.buffer.fetchPage(rec.pageId);
      Buffer.from(rec.before, "hex").copy(frame.data, rec.offset);
      db.markDirty(frame, clrLSN);
      current.lastLSN = clrLSN;
      clrCount++;
    } else if (rec.type === "clr") {
      if (rec.undoNextLSN === NULL_LSN) {
        finishTxn(db, losers, current, undoneTxnIds);
      } else {
        current.lastLSN = rec.undoNextLSN;
      }
    } else if (rec.type === "begin") {
      finishTxn(db, losers, current, undoneTxnIds);
    } else if (rec.prevLSN === NULL_LSN) {
      finishTxn(db, losers, current, undoneTxnIds);
    } else {
      current.lastLSN = rec.prevLSN;
    }
  }
  return { txnIds: undoneTxnIds, clrCount };
}

function finishTxn(
  db: Database,
  losers: Map<TxnId, TxnTableEntry>,
  entry: TxnTableEntry,
  undoneTxnIds: TxnId[],
): void {
  db.log.append({ type: "end", txnId: entry.txnId, prevLSN: entry.lastLSN });
  losers.delete(entry.txnId);
  db.txnTable.delete(entry.txnId);
  undoneTxnIds.push(entry.txnId);
}

export function undoTransaction(db: Database, txnId: TxnId): void {
  const txn = db.txnTable.get(txnId);
  if (!txn) return;
  let lsn = txn.lastLSN;
  while (lsn !== NULL_LSN) {
    const rec = db.log.getRecord(lsn);
    if (rec.type === "update") {
      const clrLSN = db.log.append({
        type: "clr",
        txnId,
        prevLSN: txn.lastLSN,
        undoNextLSN: rec.prevLSN,
        pageId: rec.pageId,
        offset: rec.offset,
        before: rec.after,
        after: rec.before,
      });
      const frame = db.buffer.fetchPage(rec.pageId);
      Buffer.from(rec.before, "hex").copy(frame.data, rec.offset);
      db.markDirty(frame, clrLSN);
      txn.lastLSN = clrLSN;
      lsn = rec.prevLSN;
    } else if (rec.type === "clr") {
      lsn = rec.undoNextLSN;
    } else {
      lsn = rec.prevLSN;
    }
  }
}
