/**
 * Acceptance scenario: 5 transactions (T1,T2 committed; T3,T4,T5 in flight),
 * a fuzzy checkpoint, partial page flushes (steal + no-force), then a crash.
 *
 * Deterministic LSN trace (single-node logical clock):
 *
 *   1  T1 UPD P1.x=100          9  T3 UPD P1.z=600     (P1 flushed -> steal)
 *   2  T2 UPD P2.y=300         10  T4 UPD P3.w=800
 *   3  T3 UPD P3.z=500         11  T1 COMMIT
 *   4  T1 UPD P2.x=200         12  T1 END
 *   5  T2 UPD P3.y=400         13  T5 UPD P2.z=900
 *   6  T2 COMMIT               14  T4 UPD P1.w=700
 *   7  T2 END                  15  T3 UPD P3.z=501
 *   8  CHECKPOINT                  *** CRASH ***
 *
 * Flush schedule before the crash:
 *   - P3 flushed after LSN 5  (contains T3's UNCOMMITTED z=500 -> needs undo)
 *   - P1 flushed after LSN 9  (contains T3's UNCOMMITTED z=600 -> needs undo)
 *   - P2 never flushed        (loses T1/T2 COMMITTED data    -> needs redo)
 */

import type { LSN } from './log-record.ts';
import { WalManager } from './wal.ts';
import { BufferPool, Disk, LatchManager } from './storage.ts';
import { TransactionManager } from './transaction-manager.ts';

export interface ScenarioResult {
  wal: WalManager;
  disk: Disk;
  pool: BufferPool;
  tm: TransactionManager;
  checkpointLSN: LSN;
  /** WAL file content as found on disk after the crash. */
  walFile: string;
}

export const EXPECTED = {
  winners: [1, 2] as const,
  losers: [3, 4, 5] as const,
  /** Final durable page contents after a correct recovery. */
  pages: {
    P1: { x: 100 },
    P2: { x: 200, y: 300 },
    P3: { y: 400 },
  },
};

export function buildCrashScenario(): ScenarioResult {
  const wal = new WalManager();
  const disk = new Disk();
  const pool = new BufferPool(disk, wal);
  const tm = new TransactionManager(wal, pool, new LatchManager());

  const t1 = tm.begin();
  const t2 = tm.begin();
  const t3 = tm.begin();

  tm.update(t1, 'P1', 'x', 100); // 1
  tm.update(t2, 'P2', 'y', 300); // 2
  tm.update(t3, 'P3', 'z', 500); // 3
  tm.update(t1, 'P2', 'x', 200); // 4
  tm.update(t2, 'P3', 'y', 400); // 5
  tm.commit(t2); // 6,7

  const checkpointLSN = tm.checkpoint(); // 8

  const t4 = tm.begin();
  const t5 = tm.begin();

  tm.update(t3, 'P1', 'z', 600); // 9
  // Steal: flush P3 (uncommitted T3.z=500) and P1 (uncommitted T3.z=600).
  wal.flush(9);
  pool.flushPage('P3');
  pool.flushPage('P1');

  tm.update(t4, 'P3', 'w', 800); // 10
  tm.commit(t1); // 11,12
  tm.update(t5, 'P2', 'z', 900); // 13
  tm.update(t4, 'P1', 'w', 700); // 14
  tm.update(t3, 'P3', 'z', 501); // 15

  // No-force: the whole log is made durable, dirty pages are NOT flushed.
  wal.flush(wal.getLastLSN());
  const walFile = wal.persistToFile();

  // Crash: volatile buffer pool is lost; disk keeps whatever was flushed.
  pool.crash();

  return { wal, disk, pool, tm, checkpointLSN, walFile };
}
