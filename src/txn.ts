import { LogManager } from "./log.ts";
import { PageStore } from "./pagestore.ts";
import { LockManager } from "./locks.ts";
import type { LSN, PageId, TxnId, TxnTableEntry } from "./types.ts";

export type TxnStatus = "active" | "committed" | "aborted";

interface TxnState {
  id: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
  firstLSN: LSN;
  savepoints: Map<string, LSN>;
}

export class TransactionManager {
  private nextTxn = 1;
  private txns = new Map<TxnId, TxnState>();
  readonly log: LogManager;
  readonly pages: PageStore;
  readonly locks: LockManager;

  constructor(log: LogManager, pages: PageStore, locks: LockManager) {
    this.log = log;
    this.pages = pages;
    this.locks = locks;
  }

  begin(): TxnId {
    const id = this.nextTxn++;
    this.txns.set(id, { id, status: "active", lastLSN: 0, firstLSN: 0, savepoints: new Map() });
    return id;
  }

  private state(txn: TxnId): TxnState {
    const t = this.txns.get(txn);
    if (!t || t.status !== "active") throw new Error(`txn ${txn} is not active`);
    return t;
  }

  update(txn: TxnId, page: PageId, after: number): LSN {
    const t = this.state(txn);
    this.locks.acquire(page, txn);
    const before = this.pages.readForTxn(page);
    const rec = this.log.append({ type: "update", txn, page, before, after, prevLSN: t.lastLSN });
    this.pages.applyUpdate(page, after, rec.lsn, txn);
    if (!t.firstLSN) t.firstLSN = rec.lsn;
    t.lastLSN = rec.lsn;
    return rec.lsn;
  }

  /** Bind a savepoint name to the txn-internal LSN; reusing a name overwrites the old binding. */
  savepoint(txn: TxnId, name: string): LSN {
    const t = this.state(txn);
    t.savepoints.set(name, t.lastLSN);
    return t.lastLSN;
  }

  /** Undo only uncompensated updates after the savepoint; the txn stays alive. */
  rollbackTo(txn: TxnId, name: string): { undone: number; clrs: LSN[] } {
    const t = this.state(txn);
    const sp = t.savepoints.get(name);
    if (sp === undefined) throw new Error(`txn ${txn}: no savepoint named "${name}"`);
    return this.undoUntil(t, sp);
  }

  private undoUntil(t: TxnState, stopLSN: LSN): { undone: number; clrs: LSN[] } {
    const clrs: LSN[] = [];
    let cur = t.lastLSN;
    while (cur > stopLSN) {
      const rec = this.log.get(cur);
      if (!rec) throw new Error(`txn ${t.id}: log record ${cur} missing (log truncated too far?)`);
      if (rec.type === "update") {
        if (rec.txn !== t.id) throw new Error(`txn ${t.id}: corrupted undo chain at lsn ${cur}`);
        const clr = this.log.append({
          type: "clr",
          txn: t.id,
          page: rec.page,
          before: rec.after,
          after: rec.before,
          prevLSN: t.lastLSN,
          undoNext: rec.prevLSN,
        });
        this.pages.applyUpdate(rec.page, rec.before, clr.lsn, t.id);
        clrs.push(clr.lsn);
        t.lastLSN = clr.lsn;
        cur = rec.prevLSN;
      } else if (rec.type === "clr") {
        cur = rec.undoNext; // already compensated — never undo a CLR
      } else if (rec.type === "abort" || rec.type === "commit") {
        cur = rec.prevLSN; // walk past the abort marker into the txn chain
      } else {
        break;
      }
    }
    return { undone: clrs.length, clrs };
  }

  commit(txn: TxnId): void {
    const t = this.state(txn);
    const c = this.log.append({ type: "commit", txn, prevLSN: t.lastLSN });
    t.lastLSN = c.lsn;
    this.pages.onCommit(txn);
    this.locks.releaseAll(txn);
    const e = this.log.append({ type: "end", txn, prevLSN: t.lastLSN });
    t.lastLSN = e.lsn;
    t.status = "committed";
  }

  /** Full abort: undo everything, skipping updates already compensated by CLRs. */
  abort(txn: TxnId): { clrs: LSN[] } {
    const t = this.state(txn);
    const a = this.log.append({ type: "abort", txn, prevLSN: t.lastLSN });
    t.lastLSN = a.lsn;
    const { clrs } = this.undoUntil(t, 0);
    this.locks.releaseAll(txn);
    const e = this.log.append({ type: "end", txn, prevLSN: t.lastLSN });
    t.lastLSN = e.lsn;
    t.status = "aborted";
    this.pages.onAbortEnd(txn);
    return { clrs };
  }

  txnTable(): TxnTableEntry[] {
    const out: TxnTableEntry[] = [];
    for (const t of this.txns.values()) {
      if (t.status === "active") {
        out.push({ txn: t.id, status: t.status, lastLSN: t.lastLSN, firstLSN: t.firstLSN });
      }
    }
    return out;
  }

  checkpoint(): LSN {
    return this.log.checkpoint(this.txnTable(), this.pages.dirtyPageTable());
  }
}
