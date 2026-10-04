import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { WriteAheadLog } from '../src/wal.ts';
import { BufferPool, DiskStorage } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';
import { CheckpointManager } from '../src/checkpoint.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { SnapshotManager } from '../src/snapshot.ts';

export interface TestEnv {
  root: string;
  wal: WriteAheadLog;
  storage: DiskStorage;
  pool: BufferPool;
  txns: TransactionManager;
  checkpoints: CheckpointManager;
  snapshots: SnapshotManager;
  cleanup: () => void;
}

/** A full database instance rooted at a fresh temp directory. */
export function makeEnv(prefix = 'aries-test-'): TestEnv {
  const root = mkdtempSync(join(tmpdir(), prefix));
  const wal = new WriteAheadLog(join(root, 'wal.log'));
  const storage = new DiskStorage(join(root, 'data'));
  const pool = new BufferPool(storage, () => wal.flush());
  const txns = new TransactionManager(wal, pool);
  const checkpoints = new CheckpointManager(wal, txns, pool);
  const snapshots = new SnapshotManager(
    storage,
    pool,
    wal,
    join(root, 'snapshots'),
    join(root, 'archive'),
  );
  return {
    root,
    wal,
    storage,
    pool,
    txns,
    checkpoints,
    snapshots,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  };
}

/** Re-open the same directories after a simulated crash (no flush). */
export function reopenAfterCrash(env: TestEnv): {
  wal: WriteAheadLog;
  storage: DiskStorage;
  pool: BufferPool;
  recovery: RecoveryManager;
} {
  const wal = new WriteAheadLog(join(env.root, 'wal.log'));
  const storage = new DiskStorage(join(env.root, 'data'));
  const pool = new BufferPool(storage, () => wal.flush());
  const recovery = new RecoveryManager(wal, pool);
  return { wal, storage, pool, recovery };
}
