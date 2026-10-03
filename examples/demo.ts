/**
 * ARIES crash-recovery demo.
 *
 * Scenario (the acceptance boundary):
 *   - 5 transactions: T1, T2 committed; T3, T4, T5 uncommitted.
 *   - A fuzzy checkpoint is taken after the commits, while T3 is active.
 *   - A dirty page containing UNCOMMITTED data is stolen to disk.
 *   - Sudden power loss. Then restart recovery: Analysis -> Redo -> Undo.
 *
 * Run with:  node examples/demo.ts        (Node >= 22.6, strips types natively)
 */
import { DatabaseSystem } from '../src/system.ts';
import { FaultInjector } from '../src/fault.ts';
import { LossyChannel, Replica } from '../src/replication.ts';
import { LogType } from '../src/types.ts';
import type { LogRecord } from '../src/types.ts';

const line = (s = '') => console.log(s);

function fmtLog(records: LogRecord[]): string {
  return records
    .map((r) => {
      const txn = 'txnId' in r ? `T${r.txnId}` : '-';
      const extra =
        r.type === LogType.Update || r.type === LogType.Clr
          ? ` P${r.pageId} ${'beforeImage' in r ? r.beforeImage : ''}->${'afterImage' in r ? r.afterImage : ''}`
          : '';
      const undoNext = r.type === LogType.Clr ? ` undoNext=${r.undoNextLSN}` : '';
      return `  [LSN ${String(r.lsn).padStart(2)}] ${r.type.padEnd(16)} ${txn}${extra}${undoNext}`;
    })
    .join('\n');
}

line('=== ARIES crash recovery demo ===');
line();

const system = new DatabaseSystem();
const faults = new FaultInjector(system);

// --- Normal processing -----------------------------------------------------
line('--- Phase 0: normal processing (WAL is being written) ---');
const t1 = system.begin();
const t2 = system.begin();
system.update(t1, 1, 100); // committed later
system.update(t2, 3, 300); // committed later
system.update(t1, 2, 200);
system.commit(t1);
system.commit(t2);
line(`committed: T${t1}, T${t2}`);

const t3 = system.begin();
system.update(t3, 1, 111); // uncommitted, overwrites T1's committed value
line(`T${t3} updates P1 -> 111 (uncommitted)`);

line('--- fuzzy checkpoint (T3 still active) ---');
system.checkpoint();

const t4 = system.begin();
const t5 = system.begin();
system.update(t4, 2, 222); // uncommitted
system.update(t5, 4, 444); // uncommitted
system.update(t5, 5, 555); // uncommitted
line(`T${t4} updates P2 -> 222 (uncommitted)`);
line(`T${t5} updates P4 -> 444, P5 -> 555 (uncommitted)`);

// Background WAL flush, then steal ONE dirty page: uncommitted data of T3
// reaches disk, while the other dirty pages stay in the volatile buffer pool.
system.logManager.flushAll();
system.bufferPool.flushPage(1);
line('page P1 stolen to disk (STEAL: uncommitted P1=111 is now durable)');
line();
line('--- durable WAL at crash time ---');
line(fmtLog(system.disk.readLog()));
line();

// --- Crash -----------------------------------------------------------------
faults.powerLoss();
line('*** POWER LOSS: log tail, buffer pool, locks and txn table are gone ***');
line();

// --- Recovery ----------------------------------------------------------------
line('--- Phase 1+2+3: restart recovery (Analysis -> Redo -> Undo) ---');
const report = system.recover();

line(`Analysis: checkpoint at LSN ${report.analysis.checkpointLSN}, redoLSN=${report.analysis.redoLSN}`);
line(
  `Analysis: txnTable = { ${[...report.analysis.txnTable.values()]
    .map((e) => `T${e.txnId}:${e.status}@${e.lastLSN}`)
    .join(', ')} }`,
);
line(
  `Analysis: dirtyPageTable = { ${[...report.analysis.dirtyPageTable.entries()]
    .map(([p, l]) => `P${p}:recLSN=${l}`)
    .join(', ')} }`,
);
line(`Redo: applied=${report.redo.applied} skipped=${report.redo.skipped} (history repeated)`);
line(
  `Undo: losers=[${report.undo.losers.map((t) => 'T' + t)}] winners=[${report.undo.winners.map((t) => 'T' + t)}] CLRs written=${report.undo.clrCount}`,
);
line();

line('--- final page values on disk ---');
for (const pageId of [1, 2, 3, 4, 5]) {
  line(`  P${pageId} = ${system.disk.pageValue(pageId)} (pageLSN=${system.disk.pageLSN(pageId)})`);
}
line();

const expected = new Map([
  [1, 100], // T1 committed, T3's 111 rolled back
  [2, 200], // T1 committed, T4's 222 rolled back
  [3, 300], // T2 committed
  [4, 0],   // T5 never committed
  [5, 0],   // T5 never committed
]);
let ok = true;
for (const [pageId, want] of expected) {
  const got = system.disk.pageValue(pageId);
  if (got !== want) {
    ok = false;
    line(`  MISMATCH P${pageId}: want ${want}, got ${got}`);
  }
}
line(ok ? 'RESULT: committed data persisted, uncommitted data rolled back. OK' : 'RESULT: FAILED');
line(`Leak check: txnTable empty=${report.finalTxnTable.size === 0}, pinned=${system.bufferPool.pinnedCount}, locks=${system.lockManager.lockCount}, bufferedLog=${system.logManager.bufferedCount}`);
line();

// --- Fault-injection showcase ------------------------------------------------
line('=== Fault-injection showcase ===');

// 1. Disk full during commit: commit fails cleanly, retry heals.
{
  const sys = new DatabaseSystem();
  const inj = new FaultInjector(sys);
  const t = sys.begin();
  sys.update(t, 1, 42);
  inj.setDiskFull(true);
  let commitFailed = false;
  try {
    sys.commit(t);
  } catch {
    commitFailed = true;
  }
  inj.setDiskFull(false);
  sys.commit(t); // retry succeeds, no corruption
  sys.flushAllPages();
  line(`disk-full commit: failed=${commitFailed}, retried ok, P1=${sys.disk.pageValue(1)}`);
}

// 2. Packet loss on log shipping: replica truncates at the gap and recovers.
{
  const sys = new DatabaseSystem();
  const a = sys.begin();
  sys.update(a, 1, 7);
  sys.commit(a);
  const b = sys.begin();
  sys.update(b, 2, 9);
  sys.commit(b);
  const wal = sys.disk.readLog();
  const channel = new LossyChannel();
  // Drop txn B's update record: everything behind the gap is discarded.
  const updateB = wal.find((r) => r.type === LogType.Update && 'txnId' in r && r.txnId === b);
  if (updateB) channel.dropLSNs([updateB.lsn]);
  const replica = new Replica();
  replica.receive(channel.ship(wal));
  line(`packet loss: dropped LSN ${updateB?.lsn}, replica contiguous up to LSN ${replica.contiguousLSN}, gap=${replica.hasGap}`);
  // Self-healing: the replica recovers from its contiguous prefix.
  const { LogManager } = await import('../src/logManager.ts');
  const { BufferPool } = await import('../src/bufferPool.ts');
  const { recover } = await import('../src/recovery.ts');
  const lm = new LogManager(replica.disk);
  const bp = new BufferPool(replica.disk, lm);
  recover(replica.disk, lm, bp);
  line(`replica recovered: P1=${replica.disk.pageValue(1)} (txn A healed), P2=${replica.disk.pageValue(2)} (txn B lost with the packet)`);
}

line();
line('demo complete.');
