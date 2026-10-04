import { LogManager } from "./logManager.ts";
import { PageStore } from "./pageStore.ts";
import { NULL_LSN } from "./types.ts";
import type { LSN, PageId, TxnId, TxnStatus } from "./types.ts";

export class UnknownSavepointError extends Error {
  constructor(name: string, txnId: TxnId) {
    super(`txn ${txnId} has no savepoint named "${name}"`);
    this.name = "UnknownSavepointError";
  }
}

export interface TxnHandle {
  txnId: TxnId;
  firstLSN: LSN;
  lastLSN: LSN;
  status: TxnStatus;
  savepoints: Map<string, LSN>; // name -> txn-internal LSN binding
}

export class TransactionManager {
  private log: LogManager;
  private pages: PageStore;
  private nextTxnId: TxnId;
  private txns = new Map<TxnId, TxnHandle>();

  constructor(log: LogManager, pages: PageStore, nextTxnId = 1) {
    this.log = log;
    this.pages = pages;
    this.nextTxnId = nextTxnId;
  }

  get activeTxns(): TxnHandle[] {
    return [...this.txns.values()].filter(
      (t) => t.status === "running" || t.status === "aborting",
    );
  }

  handle(txnId: TxnId): TxnHandle {
    const t = this.txns.get(txnId);
    if (!t) throw new Error(`unknown txn ${txnId}`);
    return t;
  }

  private assertRunning(t: TxnHandle): void {
    if (t.status !== "running") {
      throw new Error(`txn ${t.txnId} is ${t.status}, expected running`);
    }
  }

  begin(): TxnId {
    const txnId = this.nextTxnId++;
    const lsn = this.log.allocLSN();
    this.log.append({ type: "BEGIN", lsn, txnId, prevLSN: NULL_LSN });
    this.txns.set(txnId, {
      txnId,
      firstLSN: lsn,
      lastLSN: lsn,
      status: "running",
      savepoints: new Map(),
    });
    return txnId;
  }

  update(txnId: TxnId, pageId: PageId, after: string): LSN {
    const t = this.handle(txnId);
    this.assertRunning(t);
    const before = this.pages.readForTxn(pageId, txnId); // also takes the lock
    const lsn = this.log.allocLSN();
    this.log.append({
      type: "UPDATE",
      lsn,
      txnId,
      pageId,
      before,
      after,
      prevLSN: t.lastLSN,
    });
    this.pages.apply(pageId, txnId, after, lsn);
    t.lastLSN = lsn;
    return lsn;
  }

  /** Bind `name` to the txn's current LSN; reusing a name overwrites the old binding. */
  savepoint(txnId: TxnId, name: string): LSN {
    const t = this.handle(txnId);
    this.assertRunning(t);
    const savepointLSN = t.lastLSN;
    t.savepoints.set(name, savepointLSN); // overwrite on name reuse
    const lsn = this.log.allocLSN();
    this.log.append({
      type: "SAVEPOINT",
      lsn,
      txnId,
      name,
      savepointLSN,
      prevLSN: t.lastLSN,
    });
    t.lastLSN = lsn;
    return savepointLSN;
  }

  /**
   * Undo every not-yet-compensated UPDATE strictly after the savepoint,
   * writing one CLR (with undoNextLSN) per undone update. Idempotent: a
   * repeat with no intervening updates writes no CLRs, because the walk
   * follows CLR.undoNextLSN past already-compensated updates.
   */
  rollbackTo(txnId: TxnId, name: string): LSN[] {
    const t = this.handle(txnId);
    this.assertRunning(t);
    const target = t.savepoints.get(name);
    if (target === undefined) throw new UnknownSavepointError(name, txnId);
    const clrs = this.undoChain(t, (lsn) => lsn > target);
    return clrs;
  }

  /**
   * Walk the txn chain backwards from lastLSN, compensating UPDATE records
   * selected by `shouldUndo`. CLRs are never undone; their undoNextLSN is
   * followed instead. Returns the LSNs of the CLRs written.
   */
  private undoChain(t: TxnHandle, shouldUndo: (lsn: LSN) => boolean): LSN[] {
    const clrLSNs: LSN[] = [];
    let lsn = t.lastLSN;
    while (lsn !== NULL_LSN) {
      const rec = this.log.read(lsn);
      if (rec.type === "UPDATE") {
        if (shouldUndo(rec.lsn)) {
          const clrLSN = this.log.allocLSN();
          this.log.append({
            type: "CLR",
            lsn: clrLSN,
            txnId: t.txnId,
            pageId: rec.pageId,
            after: rec.before, // redo image of this undo
            prevLSN: t.lastLSN,
            undoNextLSN: rec.prevLSN,
            undidLSN: rec.lsn,
          });
          this.pages.apply(rec.pageId, t.txnId, rec.before, clrLSN);
          t.lastLSN = clrLSN;
          clrLSNs.push(clrLSN);
        }
        lsn = rec.prevLSN; // updates at/below the savepoint are kept, walk on
      } else if (rec.type === "CLR") {
        lsn = rec.undoNextLSN; // skip already-compensated updates
      } else if (rec.type === "BEGIN") {
        break;
      } else if (
        rec.type === "SAVEPOINT" ||
        rec.type === "ABORT" ||
        rec.type === "COMMIT" ||
        rec.type === "END"
      ) {
        // SAVEPOINT marker, or ABORT written at the start of a full abort:
        // keep walking the chain via prevLSN.
        lsn = rec.prevLSN;
      } else {
        throw new Error(`unexpected ${rec.type} record in txn chain`);
      }
    }
    return clrLSNs;
  }

  commit(txnId: TxnId): void {
    const t = this.handle(txnId);
    this.assertRunning(t);
    const commitLSN = this.log.allocLSN();
    this.log.append({ type: "COMMIT", lsn: commitLSN, txnId, prevLSN: t.lastLSN });
    t.lastLSN = commitLSN;
    t.status = "committed";
    this.pages.publishCommitted(txnId); // external readers can now see the writes
    this.pages.releaseAll(txnId); // strict: locks held until here
    const endLSN = this.log.allocLSN();
    this.log.append({ type: "END", lsn: endLSN, txnId, prevLSN: t.lastLSN });
    t.lastLSN = endLSN;
    t.status = "ended";
  }

  /** Full abort: undo everything, skipping records already compensated by CLRs. */
  abort(txnId: TxnId): void {
    const t = this.handle(txnId);
    if (t.status !== "running" && t.status !== "aborting") {
      throw new Error(`txn ${txnId} is ${t.status}, cannot abort`);
    }
    t.status = "aborting";
    const abortLSN = this.log.allocLSN();
    this.log.append({ type: "ABORT", lsn: abortLSN, txnId, prevLSN: t.lastLSN });
    t.lastLSN = abortLSN;
    this.undoChain(t, () => true); // CLR-aware: never re-undoes
    this.pages.releaseAll(txnId);
    const endLSN = this.log.allocLSN();
    this.log.append({ type: "END", lsn: endLSN, txnId, prevLSN: t.lastLSN });
    t.lastLSN = endLSN;
    t.status = "ended";
  }
}
