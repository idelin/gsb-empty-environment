/**
 * End-to-end demo of the ARIES recovery engine and the incremental
 * snapshot / checkpoint archive manager.
 *
 * Scenario (the acceptance boundary):
 *   - 5 transactions: T1, T2 commit; T3, T4, T5 stay uncommitted.
 *   - A fuzzy checkpoint is taken in the middle of the workload.
 *   - The system crashes: two dirty pages were stolen to disk (one of them
 *     holding UNCOMMITTED data), the rest of the buffer pool is lost.
 *   - Recovery runs analysis -> redo -> undo and must persist committed
 *     data while rolling back every uncommitted change.
 *   - Afterwards: incremental snapshots, archiving and restore.
 */
import { rmSync } from 'node:fs';
import { join } from 'node:path';
import { WriteAheadLog } from '../src/wal.ts';
import { BufferPool, DiskStorage } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';
import { CheckpointManager } from '../src/checkpoint.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { SnapshotManager } from '../src/snapshot.ts';
import { LogType } from '../src/types.ts';

const root = join(process.cwd(), 'demo-data');
rmSync(root, { recursive: true, force: true });

const wal = new WriteAheadLog(join(root, 'wal.log'));
const storage = new DiskStorage(join(root, 'data'));
const pool = new BufferPool(storage, () => wal.flush());
const txns = new TransactionManager(wal, pool);
const checkpoints = new CheckpointManager(wal, txns, pool);

const line = (s = '') => console.log(s);

line('=== Phase 0: normal processing (5 transactions, 1 checkpoint) ===');
const t1 = txns.begin();
txns.update(t1, 1, 'A1');
txns.update(t1, 2, 'B1');
const t2 = txns.begin();
txns.update(t2, 3, 'C1');
line(`T${t1}: UPDATE p1=A1, p2=B1   T${t2}: UPDATE p3=C1`);

const ckptLsn = checkpoints.checkpoint();
line(`--- fuzzy checkpoint @ LSN ${ckptLsn} (TT + DPT archived into WAL) ---`);

const t3 = txns.begin();
txns.update(t3, 1, 'A3');
txns.update(t1, 2, 'B1x');
txns.commit(t1);
line(`T${t3}: UPDATE p1=A3 (uncommitted)   T${t1}: UPDATE p2=B1x, COMMIT`);

txns.update(t2, 3, 'C2');
txns.commit(t2);
line(`T${t2}: UPDATE p3=C2, COMMIT`);

const t4 = txns.begin();
txns.update(t4, 4, 'D4');
const t5 = txns.begin();
txns.update(t5, 5, 'E5');
txns.update(t5, 1, 'A5');
line(`T${t4}: UPDATE p4=D4 (uncommitted)   T${t5}: UPDATE p5=E5, p1=A5 (uncommitted)`);

pool.flushPage(2); // committed page stolen to disk
pool.flushPage(1); // page holding UNCOMMITTED data stolen to disk
line('buffer pool: pages 1,2 stolen to disk; pages 3,4,5 lost on crash');
line('');
line('*** CRASH *** (unflushed buffer pool and WAL tail are lost)');
line('');

// ---- reboot: fresh instances over the same files ----
const wal2 = new WriteAheadLog(join(root, 'wal.log'));
const storage2 = new DiskStorage(join(root, 'data'));
const pool2 = new BufferPool(storage2, () => wal2.flush());
const recovery = new RecoveryManager(wal2, pool2);
const report = recovery.recover();

// Post-crash components bind to the recovered pool, not the dead one.
const txns2 = new TransactionManager(wal2, pool2);
const snapshots = new SnapshotManager(
  storage2,
  pool2,
  wal2,
  join(root, 'snapshots'),
  join(root, 'archive'),
);


line('=== Phase 1: ANALYSIS ===');
line(`  last checkpoint @ LSN ${report.analysis.checkpointLSN}`);
line(`  transaction table (losers): ${JSON.stringify(report.analysis.txnTable)}`);
line(`  dirty page table: ${JSON.stringify(report.analysis.dirtyPages)}`);
line(`  redo starts @ LSN ${report.analysis.redoLSN}`);
line('');

line('=== Phase 2: REDO (repeat history) ===');
line(`  applied: [${report.redo.applied.join(', ')}]`);
line(`  skipped (already on disk): [${report.redo.skipped.join(', ')}]`);
line('');

line('=== Phase 3: UNDO (losers, reverse scan, CLRs) ===');
line(`  loser transactions: [${report.undo.losers.join(', ')}]`);
line(`  CLRs written: [${report.undo.clrs.join(', ')}]`);
line(`  transactions ended: [${report.undo.ended.join(', ')}]`);
line('');

line('=== Final persisted state ===');
for (const pageId of storage2.listPageIds()) {
  const page = storage2.readPage(pageId)!;
  line(`  page ${pageId}: data=${JSON.stringify(page.data)} pageLSN=${page.pageLSN}`);
}
line('  expected: p1=A1 (T1), p2=B1x (T1), p3=C2 (T2), p4/p5 rolled back');
line('');

line('=== Incremental snapshots & checkpoint archive ===');
const snap1 = await snapshots.beginSnapshot();
const m1 = await snapshots.completeSnapshot();
line(`snapshot ${snap1}: ${Object.keys(m1.pages).length} pages archived (full)`);

const t6 = txns2.begin();
txns2.update(t6, 1, 'A1-snap2');
txns2.commit(t6);
const snap2 = await snapshots.beginSnapshot();
const m2 = await snapshots.completeSnapshot();
const reused = Object.values(m2.pages).filter((r) => !r.file.startsWith(`snapshot-${snap2}/`)).length;
line(`snapshot ${snap2}: ${Object.keys(m2.pages).length} pages, ${reused} reused from snapshot ${snap1} (incremental)`);

const t7 = txns2.begin();
txns2.update(t7, 2, 'B1x-snap3');
txns2.commit(t7);
await snapshots.beginSnapshot();
await snapshots.completeSnapshot();

const archived = await snapshots.prune(1);
line(`prune(keepLast=1): archived snapshots [${archived.join(', ')}]`);
line(`  live snapshots: [${snapshots.listSnapshots().join(', ')}], archives: [${snapshots.listArchives().join(', ')}]`);

const restored = new DiskStorage(join(root, 'restored-from-archive'));
snapshots.restore(1, restored);
line(`restore(snapshot 1) from archive: p1=${JSON.stringify(restored.readPage(1)!.data)} (baseline preserved)`);

pool2.flushAll(); // no-force policy: dirty pages linger until a flush
const leak = snapshots.debugState();
line('');
line(`leak check: active=${leak.active} staged=${leak.stagedCount} tmpFiles=${leak.tmpFiles.length} walBuffered=${wal2.bufferedCount} dirtyPages=${pool2.dirtyCount}`);
line(`demo data left in ${root} for inspection`);
