import { FileWALStorage, LogManager } from './wal.ts';
import { BufferPool, Disk } from './storage.ts';
import { TransactionManager } from './txn.ts';
import { RecoveryManager } from './recovery.ts';
import type { RecoveryReport } from './recovery.ts';

export interface AcceptanceScenario {
  walPath: string;
  disk: Disk;
  committed: { t1: number; t2: number };
  uncommitted: { t3: number; t4: number; t5: number };
}

// Builds the acceptance WAL: 2 committed txns (T1, T2), 3 uncommitted txns
// (T3, T4, T5), a fuzzy checkpoint after T3/T4 started, a stolen dirty page
// carrying uncommitted data, then a crash.
export function buildAcceptanceScenario(walPath: string): AcceptanceScenario {
  const storage = new FileWALStorage(walPath, true);
  const log = new LogManager(storage);
  const disk = new Disk();
  const bp = new BufferPool(disk, 16, log);
  const tm = new TransactionManager(log, bp);

  // T1: committed. Writes P1.k1=10, P2.k1=20.
  const t1 = tm.begin();
  tm.update(t1, 1, 'k1', 10);
  tm.update(t1, 2, 'k1', 20);
  tm.commit(t1);

  // T2: committed. Writes P3.k1=30. Pages NOT forced to disk (no-force).
  const t2 = tm.begin();
  tm.update(t2, 3, 'k1', 30);
  tm.commit(t2);

  // T3: uncommitted. Overwrites P1.k1 with 11.
  const t3 = tm.begin();
  tm.update(t3, 1, 'k1', 11);

  // T4: uncommitted. Writes P2.k1=21 and P4.k1=40.
  const t4 = tm.begin();
  tm.update(t4, 2, 'k1', 21);
  tm.update(t4, 4, 'k1', 40);

  // Fuzzy checkpoint with T3, T4 active.
  tm.checkpoint();

  // T5: uncommitted, starts after the checkpoint. Overwrites P3.k1 with 31.
  const t5 = tm.begin();
  tm.update(t5, 3, 'k1', 31);

  // steal: flush P1 while it carries T3's UNCOMMITTED write.
  bp.flushPage(1);

  // Make the whole log durable, then crash: buffer pool is lost.
  log.flush();
  log.crash();
  bp.discardAll();

  return { walPath, disk, committed: { t1, t2 }, uncommitted: { t3, t4, t5 } };
}

export function recoverAcceptanceScenario(
  walPath: string,
  disk: Disk,
): { report: RecoveryReport; log: LogManager; bp: BufferPool } {
  const log = new LogManager(new FileWALStorage(walPath));
  const bp = new BufferPool(disk, 16, log);
  const report = new RecoveryManager(log, bp).recover();
  return { report, log, bp };
}
