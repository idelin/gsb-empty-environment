import { MetricsRegistry } from "../src/metrics/registry.ts";
import { BufferPool } from "../src/storage/bufferPool.ts";
import { DiskManager } from "../src/storage/disk.ts";
import { TransactionManager } from "../src/txn/transactionManager.ts";
import { WalWriter } from "../src/wal/wal.ts";

export const PAGE_IDS = { P1: 0, P2: 1, P3: 2, P4: 3, P5: 4 } as const;
export const INITIAL_DATA: Record<number, string> = {
  [PAGE_IDS.P1]: "A",
  [PAGE_IDS.P2]: "B",
  [PAGE_IDS.P3]: "C",
  [PAGE_IDS.P4]: "D",
  [PAGE_IDS.P5]: "E",
};

/** Expected committed state after recovery. */
export const EXPECTED_DATA: Record<number, string> = {
  [PAGE_IDS.P1]: "T1b", // T1 committed, T5's uncommitted update rolled back
  [PAGE_IDS.P2]: "T2a", // T2 committed
  [PAGE_IDS.P3]: "C",   // T3 never committed: fully undone
  [PAGE_IDS.P4]: "T2a", // T2 committed
  [PAGE_IDS.P5]: "E",   // T4 never committed: fully undone
};

export interface CrashSite {
  walPath: string;
  diskPath: string;
  /** LSNs of the log records written before the crash. */
  lastLSN: number;
}

/**
 * Builds the acceptance WAL: 5 transactions (T1,T2 commit after the
 * checkpoint; T3,T4,T5 stay uncommitted), a fuzzy checkpoint in the middle,
 * partial flushes, then a crash.
 */
export function buildCrashScenario(dir: string): CrashSite {
  const walPath = `${dir}/wal.log`;
  const diskPath = `${dir}/data.db`;
  const metrics = new MetricsRegistry();
  const disk = new DiskManager(diskPath, 4096, 16, metrics);
  for (const [pageId, data] of Object.entries(INITIAL_DATA)) {
    disk.writePage(Number(pageId), { pageLSN: 0, data });
  }
  const bufferPool = new BufferPool(disk, metrics);
  const wal = new WalWriter(walPath, metrics);
  const tm = new TransactionManager(wal, bufferPool, metrics);

  const t1 = tm.begin();
  const t2 = tm.begin();
  const t3 = tm.begin();
  tm.update(t1, PAGE_IDS.P1, "A", "T1a");
  tm.update(t2, PAGE_IDS.P2, "B", "T2a");
  tm.update(t3, PAGE_IDS.P3, "C", "T3a");

  tm.checkpoint(); // DPT={P1,P2,P3}, TT={T1,T2,T3 running}

  tm.update(t1, PAGE_IDS.P1, "T1a", "T1b");
  tm.commit(t1);
  bufferPool.flushPage(PAGE_IDS.P1); // committed P1 hits disk before crash

  tm.update(t2, PAGE_IDS.P4, "D", "T2a");
  tm.commit(t2);

  const t4 = tm.begin();
  const t5 = tm.begin();
  tm.update(t4, PAGE_IDS.P5, "E", "T4a");
  tm.update(t5, PAGE_IDS.P1, "T1b", "T5a"); // uncommitted overwrite of committed data
  tm.update(t3, PAGE_IDS.P3, "T3a", "T3b");

  bufferPool.flushPage(PAGE_IDS.P2);
  bufferPool.flushPage(PAGE_IDS.P4);

  tm.crash(); // T3, T4, T5 still active: WAL on disk, volatile state gone
  disk.close();
  return { walPath, diskPath, lastLSN: 14 };
}
