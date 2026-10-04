import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { DiskStorage, BufferPool } from "./storage.ts";
import { TransactionManager } from "./txn-manager.ts";
import { WriteAheadLog } from "./wal.ts";
import type { PageId, PageValue } from "./log-record.ts";

export interface CrashScenario {
  dir: string;
  walPath: string;
  dbPath: string;
  /** Expected durable page values after a correct recovery. */
  expected: Record<PageId, PageValue>;
  txnIds: {
    committed: number[];
    uncommitted: number[];
    abortingAtCrash: number;
  };
}

/**
 * Build the acceptance scenario:
 *
 *   T0: update P1, commit + END            (history before checkpoint)
 *   --- fuzzy CHECKPOINT ---
 *   T1: update P1, update P2, commit       (winner, not flushed to disk)
 *   T2: update P3, commit                  (winner)
 *   T3: update P1, update P4               (loser; P4 IS flushed pre-crash)
 *   T4: update P2                          (loser)
 *   T5: update P3, ABORT, CLR(P3)          (loser, crash mid-abort)
 *   --- CRASH: unflushed pages + WAL tail lost ---
 *
 * Expected post-recovery state: P1=11, P2=21, P3=31, P4=40.
 */
export async function buildCrashScenario(dir: string): Promise<CrashScenario> {
  mkdirSync(dir, { recursive: true });
  const walPath = join(dir, "wal.log");
  const dbPath = join(dir, "db.json");

  const disk = new DiskStorage(dbPath);
  disk.writePage("P1", 10, 0);
  disk.writePage("P2", 20, 0);
  disk.writePage("P3", 30, 0);
  disk.writePage("P4", 40, 0);

  const wal = new WriteAheadLog(walPath);
  const pool = new BufferPool(disk);
  const tm = new TransactionManager(wal, pool);

  // T0: fully finished before the checkpoint.
  const t0 = await tm.begin();
  await tm.update(t0, "P1", 10); // no-op-ish write to create history
  await tm.commit(t0);

  // Fuzzy checkpoint with no active transactions.
  await tm.checkpoint();

  // T1: winner — its update to P1 is never flushed before the crash,
  // so Redo must restore it.
  const t1 = await tm.begin();
  await tm.update(t1, "P1", 11);
  await tm.update(t1, "P2", 21);
  await tm.commit(t1);

  // T2: winner.
  const t2 = await tm.begin();
  await tm.update(t2, "P3", 31);
  await tm.commit(t2);

  // T3: loser touching two pages.
  const t3 = await tm.begin();
  await tm.update(t3, "P1", 12);
  await tm.update(t3, "P4", 41);

  // T4: loser touching a page also written by winner T1.
  const t4 = await tm.begin();
  await tm.update(t4, "P2", 22);

  // T5: loser that begins aborting but crashes mid-rollback — one CLR is
  // already durable, so restart-undo must skip it via undoNextLSN.
  const t5 = await tm.begin();
  await tm.update(t5, "P3", 32);
  // Partial runtime abort: write ABORT + a single CLR manually.
  await wal.append({ type: "ABORT", txnId: t5, prevLSN: wal.lastLSN });
  const abortedUpdate = wal
    .allRecords()
    .find((r) => r.type === "UPDATE" && r.txnId === t5);
  if (!abortedUpdate || abortedUpdate.type !== "UPDATE") {
    throw new Error("scenario setup: missing T5 update");
  }
  const clr = await wal.append({
    type: "CLR",
    txnId: t5,
    prevLSN: wal.lastLSN,
    pageId: abortedUpdate.pageId,
    before: abortedUpdate.after,
    after: abortedUpdate.before,
    undoNextLSN: abortedUpdate.prevLSN,
  });
  pool.applyLogged(abortedUpdate.pageId, abortedUpdate.before, clr.lsn);

  // Force the whole log durable, then selectively flush pages to create a
  // mixed disk image: committed data lost (P1), uncommitted data persisted
  // (P4), aborted-then-compensated data persisted (P3).
  tm.flushLog();
  pool.flushPage("P3"); // contains T2's committed 31 (T5's 32 already undone)
  pool.flushPage("P4"); // contains T3's UNCOMMITTED 41 — undo must fix this
  // P1 and P2 stay unflushed: disk still holds 10 / 20.

  // CRASH: `pool` and `tm` are discarded; only walPath + dbPath survive.
  return {
    dir,
    walPath,
    dbPath,
    expected: { P1: 11, P2: 21, P3: 31, P4: 40 },
    txnIds: {
      committed: [t1, t2],
      uncommitted: [t3, t4],
      abortingAtCrash: t5,
    },
  };
}

/** Reattach to the crashed database and return a ready recovery engine. */
export async function attachRecovery(scenario: CrashScenario) {
  const { RecoveryEngine } = await import("./recovery.ts");
  const disk = new DiskStorage(scenario.dbPath);
  const wal = new WriteAheadLog(scenario.walPath);
  const pool = new BufferPool(disk);
  const engine = new RecoveryEngine(wal, pool);
  return { disk, wal, pool, engine };
}
