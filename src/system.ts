import { SimulatedDisk } from './disk.ts';
import { LogManager } from './logManager.ts';
import type { AppendHook } from './logManager.ts';
import { BufferPool } from './bufferPool.ts';
import { LockManager } from './lockManager.ts';
import { recover } from './recovery.ts';
import type { RecoveryReport } from './recovery.ts';
import { LogType, TxnStatus, NULL_LSN } from './types.ts';
import type { LSN, PageId, TxnId, TxnStatus as TxnStatusT } from './types.ts';

interface TxnState {
  status: TxnStatusT;
  lastLSN: LSN;
}

/**
 * The database system under test: WAL + STEAL/NO-FORCE buffer pool + strict
 * 2PL, with fuzzy checkpointing and ARIES crash recovery.
 */
export class DatabaseSystem {
  readonly disk: SimulatedDisk;
  logManager: LogManager;
  bufferPool: BufferPool;
  lockManager: LockManager;

  private txnStates = new Map<TxnId, TxnState>();
  private nextTxnId = 1;
  private crashed = false;
  /** Fault-injection hook re-attached to every new LogManager after a crash. */
  private pendingAppendHook: AppendHook | null = null;

  constructor() {
    this.disk = new SimulatedDisk();
    this.logManager = new LogManager(this.disk);
    this.bufferPool = new BufferPool(this.disk, this.logManager);
    this.lockManager = new LockManager();
  }

  private assertRunning(): void {
    if (this.crashed) throw new Error('system is crashed; call recover() first');
  }

  private txnLastLSN(txnId: TxnId): LSN {
    return this.txnStates.get(txnId)?.lastLSN ?? NULL_LSN;
  }

  begin(): TxnId {
    this.assertRunning();
    const txnId = this.nextTxnId++;
    this.txnStates.set(txnId, { status: TxnStatus.Running, lastLSN: NULL_LSN });
    return txnId;
  }

  read(txnId: TxnId, pageId: PageId): number {
    this.assertRunning();
    this.lockManager.acquire(txnId, pageId);
    return this.bufferPool.read(pageId);
  }

  update(txnId: TxnId, pageId: PageId, value: number): LSN {
    this.assertRunning();
    const state = this.txnStates.get(txnId);
    if (!state) throw new Error(`txn ${txnId} is not active`);
    this.lockManager.acquire(txnId, pageId);
    const beforeImage = this.bufferPool.read(pageId);
    const lsn = this.logManager.append({
      type: LogType.Update,
      txnId,
      prevLSN: state.lastLSN,
      pageId,
      beforeImage,
      afterImage: value,
    });
    this.bufferPool.update(pageId, value, lsn);
    state.lastLSN = lsn;
    return lsn;
  }

  /**
   * Force-on-commit: the log is forced up to the commit record. If the force
   * fails (ENOSPC) the txn stays COMMITTING and commit() can simply be
   * retried — the commit record is appended exactly once.
   */
  commit(txnId: TxnId): void {
    this.assertRunning();
    const state = this.txnStates.get(txnId);
    if (!state) throw new Error(`txn ${txnId} is not active`);
    let commitLSN = state.lastLSN;
    if (state.status !== TxnStatus.Committing) {
      commitLSN = this.logManager.append({
        type: LogType.Commit,
        txnId,
        prevLSN: state.lastLSN,
      });
      state.status = TxnStatus.Committing;
      state.lastLSN = commitLSN;
    }
    this.logManager.flush(commitLSN); // ENOSPC leaves txn active; retry or abort
    this.logManager.append({ type: LogType.End, txnId, prevLSN: commitLSN });
    this.txnStates.delete(txnId);
    this.lockManager.releaseAll(txnId);
  }

  /** Normal abort: roll back with CLRs, exactly like recovery-time undo. */
  abort(txnId: TxnId): void {
    this.assertRunning();
    const state = this.txnStates.get(txnId);
    if (!state) return;
    if (state.status === TxnStatus.Committing) {
      throw new Error(`txn ${txnId} already has a COMMIT record; it cannot be aborted`);
    }
    const abortLSN = this.logManager.append({
      type: LogType.Abort,
      txnId,
      prevLSN: state.lastLSN,
    });
    // Walk the txn chain backwards, compensating every UPDATE with a CLR.
    const records = new Map(this.logManager.readAll().map((r) => [r.lsn, r]));
    let lsn: LSN = abortLSN;
    let lastWritten = abortLSN;
    while (lsn !== NULL_LSN) {
      const rec = records.get(lsn);
      if (!rec) break;
      if (rec.type === LogType.Update) {
        lastWritten = this.logManager.append({
          type: LogType.Clr,
          txnId,
          prevLSN: lastWritten,
          undoNextLSN: rec.prevLSN,
          pageId: rec.pageId,
          beforeImage: rec.afterImage,
          afterImage: rec.beforeImage,
        });
        this.bufferPool.update(rec.pageId, rec.beforeImage, lastWritten);
        lsn = rec.prevLSN;
      } else if (rec.type === LogType.Clr) {
        lsn = rec.undoNextLSN;
      } else if (rec.type === LogType.Abort) {
        lsn = rec.prevLSN;
      } else {
        break;
      }
    }
    this.logManager.append({ type: LogType.End, txnId, prevLSN: lastWritten });
    this.txnStates.delete(txnId);
    this.lockManager.releaseAll(txnId);
  }

  /** Fuzzy checkpoint: snapshots the txn table and dirty page table into the log. */
  checkpoint(): void {
    this.assertRunning();
    this.logManager.append({ type: LogType.BeginCheckpoint });
    const txnTable = [...this.txnStates.entries()].map(([txnId, s]) => ({
      txnId,
      status: s.status,
      lastLSN: s.lastLSN,
    }));
    this.logManager.append({
      type: LogType.EndCheckpoint,
      txnTable,
      dirtyPageTable: this.bufferPool.dirtyPages(),
    });
    this.logManager.flushAll();
  }

  /** Flushes all dirty pages to disk (used by tests to shape the pre-crash state). */
  flushAllPages(): void {
    this.bufferPool.flushAll();
  }

  setAppendHook(hook: AppendHook | null): void {
    this.pendingAppendHook = hook;
    this.logManager.setAppendHook(hook);
  }

  /** Sudden power loss: all volatile state (log tail, buffer pool, locks, TT) is gone. */
  crash(): void {
    this.logManager.crash();
    this.txnStates.clear();
    this.lockManager = new LockManager();
    this.crashed = true;
  }

  get isCrashed(): boolean {
    return this.crashed;
  }

  /** ARIES restart recovery: Analysis -> Redo -> Undo. */
  recover(): RecoveryReport {
    this.logManager = new LogManager(this.disk);
    if (this.pendingAppendHook) this.logManager.setAppendHook(this.pendingAppendHook);
    this.bufferPool = new BufferPool(this.disk, this.logManager);
    this.lockManager = new LockManager();
    const report = recover(this.disk, this.logManager, this.bufferPool);
    // Resume txn id allocation beyond anything seen in the log.
    let maxTxnId = 0;
    for (const rec of this.disk.readLog()) {
      const txnId = (rec as { txnId?: TxnId }).txnId;
      if (txnId !== undefined && txnId > maxTxnId) maxTxnId = txnId;
    }
    this.nextTxnId = Math.max(this.nextTxnId, maxTxnId + 1);
    this.crashed = false;
    return report;
  }

  get activeTxnCount(): number {
    return this.txnStates.size;
  }
}
