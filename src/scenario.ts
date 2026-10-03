import { join } from 'node:path';
import { LogManager } from './log-manager.ts';
import { StorageEngine } from './storage.ts';
import { LockManager } from './lock-manager.ts';
import { TransactionManager } from './transaction.ts';

export interface CrashScenario {
  walPath: string;
  pagesPath: string;
  checkpointLSN: number;
  committedTxns: { t1: number; t2: number };
  loserTxns: { t3: number; t4: number; t5: number };
  /** Expected durable slot values after a correct recovery. */
  expectedDisk: Record<number, Record<number, string>>;
}

/**
 * Build the acceptance WAL: five interleaved transactions, a checkpoint
 * midway, two commits (T1, T2) and three transactions (T3, T4, T5) left
 * uncommitted when the system "crashes". Page 1 (holding a mix of
 * committed and uncommitted data) is flushed to disk before the crash;
 * pages 2 and 3 exist only in the lost buffer pool, so Redo must replay
 * them and Undo must strip the losers' changes everywhere.
 */
export async function buildCrashScenario(dir: string): Promise<CrashScenario> {
  const walPath = join(dir, 'wal.log');
  const pagesPath = join(dir, 'pages.json');
  const log = new LogManager(walPath, { truncate: true });
  const storage = new StorageEngine(log, { pagesFile: pagesPath });
  const locks = new LockManager();
  const tm = new TransactionManager(log, storage, locks);

  const t1 = tm.begin();
  const t2 = tm.begin();
  const t3 = tm.begin();
  const t4 = tm.begin();
  const t5 = tm.begin();

  await tm.write(t1, 1, 0, 'T1-A'); // committed, will be flushed
  await tm.write(t3, 1, 1, 'T3-x'); // uncommitted, will be flushed -> undo
  await tm.write(t2, 2, 0, 'T2-A'); // committed, never flushed -> redo
  const checkpointLSN = tm.checkpoint();
  await tm.write(t1, 2, 1, 'T1-B'); // committed, never flushed -> redo
  await tm.write(t4, 3, 0, 'T4-x'); // uncommitted, never flushed
  tm.commit(t1);
  await tm.write(t2, 3, 1, 'T2-B'); // committed, never flushed -> redo
  tm.commit(t2);
  await tm.write(t3, 2, 2, 'T3-y'); // uncommitted, never flushed
  await tm.write(t5, 3, 2, 'T5-z'); // uncommitted, never flushed
  await tm.write(t4, 1, 2, 'T4-w'); // uncommitted, will be flushed -> undo

  storage.flushPage(1); // WAL rule forces the log first
  log.flush(); // rest of the WAL reaches disk before the crash
  storage.crash(); // buffer pool (pages 2 and 3) is lost

  return {
    walPath,
    pagesPath,
    checkpointLSN,
    committedTxns: { t1, t2 },
    loserTxns: { t3, t4, t5 },
    expectedDisk: {
      1: { 0: 'T1-A', 1: '', 2: '' },
      2: { 0: 'T2-A', 1: 'T1-B', 2: '' },
      3: { 0: '', 1: 'T2-B', 2: '' },
    },
  };
}
