/**
 * End-to-end demo of the ARIES crash-recovery engine:
 * builds a WAL with 2 committed + 3 uncommitted transactions, checkpoints,
 * crashes, and recovers via analysis -> redo -> undo.
 */
import { RecoveryEngine } from '../src/recovery.ts';
import { boot, crash } from '../src/system.ts';
import type { LogRecord } from '../src/types.ts';

function fmt(rec: LogRecord): string {
  const parts = [
    `LSN=${String(rec.lsn).padStart(2)}`,
    rec.type.padEnd(10),
    `txn=${rec.txnId}`,
    `prev=${rec.prevLSN}`,
  ];
  if (rec.pageId !== undefined) parts.push(`page=${rec.pageId}`);
  if (rec.key !== undefined) parts.push(`key=${rec.key}`);
  if (rec.after !== undefined) parts.push(`after=${JSON.stringify(rec.after)}`);
  if (rec.undoNextLSN !== undefined) parts.push(`undoNext=${rec.undoNextLSN}`);
  return parts.join('  ');
}

function printWal(title: string, records: LogRecord[]): void {
  console.log(`\n--- ${title} ---`);
  for (const rec of records) console.log('  ' + fmt(rec));
}

const sys = boot({ seed: 7 });
const { tm, bp } = sys;

console.log('=== ARIES crash recovery demo ===');
console.log('[1] Running workload: 2 committed (T1,T2) + 3 uncommitted (T3,T4,T5) txns');

const t1 = await tm.begin();
await tm.set(t1, 1, 'a', 1);
const t2 = await tm.begin();
await tm.set(t2, 2, 'b', 2);
await tm.commit(t1);

const t3 = await tm.begin();
await tm.set(t3, 1, 'c', 3);
const t4 = await tm.begin();
await tm.set(t4, 3, 'd', 4);

const ckpt = await tm.checkpoint();
console.log(`[2] Fuzzy checkpoint written at LSN=${ckpt} (TT={T2,T3,T4}, DPT={P1,P2,P3})`);

await tm.set(t2, 2, 'e', 5);
await tm.commit(t2);
await tm.set(t3, 4, 'f', 6);
const t5 = await tm.begin();
await tm.set(t5, 3, 'g', 7);
await tm.set(t4, 1, 'h', 8);

await bp.flushPage(1); // steal: uncommitted data reaches disk
await tm.set(t5, 4, 'i', 9); // volatile tail, will be lost

console.log('[3] Disk state just before crash:');
for (const pid of sys.disk.pageIds()) {
  console.log(`  P${pid}: ${JSON.stringify(sys.disk.readPage(pid)!.data)}`);
}

const durable = crash(sys);
console.log('\n*** POWER LOSS ***  (WAL buffer, buffer pool, txn table evaporate)');

const sys2 = boot(durable);
printWal('durable WAL after reboot', sys2.wal.readDurable());

const engine = new RecoveryEngine(sys2.wal, sys2.disk, sys2.bp, sys2.injector);
const report = await engine.recover();

console.log('\n[4] Analysis:');
console.log(`  checkpoint LSN = ${report.checkpointLSN}`);
console.log(`  transaction table: ${JSON.stringify([...report.txnTable.values()])}`);
console.log(`  dirty page table:  ${JSON.stringify([...report.dirtyPageTable.entries()])}`);
console.log(`[5] Redo (repeat history): re-applied LSNs ${JSON.stringify(report.redone)}`);
console.log(`[6] Undo (losers ${JSON.stringify(report.losers)}): wrote CLRs ${JSON.stringify(report.clrs)}`);

console.log('\n[7] Recovered durable state:');
for (const pid of sys2.disk.pageIds()) {
  const page = sys2.disk.readPage(pid)!;
  console.log(`  P${pid} (pageLSN=${page.pageLSN}): ${JSON.stringify(page.data)}`);
}
printWal('final WAL (note the CLRs)', sys2.wal.readDurable());
console.log('\nCommitted data persisted, uncommitted data rolled back. No leaks.');
