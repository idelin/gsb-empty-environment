import { LogManager } from '../src/log.ts';
import { BufferPool, Disk } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { RecordType } from '../src/types.ts';
import assert from 'node:assert/strict';

const log = new LogManager();
const disk = new Disk();
const buffer = new BufferPool(disk);
const tm = new TransactionManager(log, buffer);

console.log('=== Phase 1: normal processing ===');
const t1 = tm.begin();
tm.update(t1, 1, 100);
tm.update(t1, 2, 50);
tm.commit(t1);
console.log('T1 committed (P1 += 100, P2 += 50)');

tm.checkpoint();
console.log('checkpoint taken (log forced, dirty pages flushed)');

const t2 = tm.begin();
tm.update(t2, 3, 7);
tm.commit(t2);
console.log('T2 committed (P3 += 7)');

const t3 = tm.begin();
tm.update(t3, 1, 1);
tm.update(t3, 4, 40);
const t4 = tm.begin();
tm.update(t4, 2, 2);
const t5 = tm.begin();
tm.update(t5, 5, 5);
tm.update(t5, 3, 70);
console.log('T3, T4, T5 active with uncommitted updates');

log.force(); // background log writer flushes WAL (group commit)
console.log('\n=== Phase 2: CRASH (buffer pool + unflushed log lost) ===');
buffer.crash();
log.crash();
console.log(`stable log records surviving crash: ${log.stableCount}`);

console.log('\n=== Phase 3: ARIES recovery ===');
const rm = new RecoveryManager(log, disk, buffer);
const result = rm.recover();

console.log('\n-- Analysis --');
console.log(`checkpoint begin LSN: ${result.analysis.checkpointBeginLSN}`);
console.log(`redo starts at LSN:  ${result.analysis.redoLSN}`);
for (const [txn, e] of result.analysis.txnTable) {
  console.log(`  txn ${txn}: ${e.status} (lastLSN=${e.lastLSN})`);
}
console.log(`dirty pages: ${[...result.analysis.dpt].map(([p, l]) => `P${p}@${l}`).join(', ')}`);

console.log('\n-- Redo (repeat history) --');
console.log(`redone: ${result.redo.redone}, skipped: ${result.redo.skipped}`);

console.log('\n-- Undo (losers, CLRs) --');
console.log(`CLRs written: ${result.undo.clrCount}`);
console.log(`txns ended:   ${result.undo.endedTxns.join(', ')}`);

console.log('\n=== Final disk state ===');
for (let p = 1; p <= 5; p++) {
  console.log(`  P${p} = ${disk.readPage(p).value}`);
}

assert.equal(disk.readPage(1).value, 100);
assert.equal(disk.readPage(2).value, 50);
assert.equal(disk.readPage(3).value, 7);
assert.equal(disk.readPage(4).value, 0);
assert.equal(disk.readPage(5).value, 0);
assert.equal(log.bufferedCount, 0);
assert.equal(buffer.dirtyCount, 0);
const clrCount = log.stableRecords().filter((r) => r.type === RecordType.CLR).length;
assert.equal(clrCount, 5);
console.log('\nAll assertions passed: committed data durable, uncommitted data rolled back.');
