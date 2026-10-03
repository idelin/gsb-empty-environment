/**
 * Runnable demo: build the crash scenario, run ARIES recovery, print proof.
 *
 *   node --experimental-strip-types src/demo.ts
 */

import { WalManager } from './wal.ts';
import { RecoveryEngine } from './recovery.ts';
import { buildCrashScenario, EXPECTED } from './scenario.ts';
import { LogRecordType } from './log-record.ts';
import type { LogRecord } from './log-record.ts';

function fmt(rec: LogRecord): string {
  const base = `LSN=${String(rec.lsn).padStart(2)} ${rec.type.padEnd(10)}`;
  if (rec.type === LogRecordType.CHECKPOINT) {
    return `${base} TT=[${rec.txnTable.map((t) => `T${t.txnId}:${t.status}@${t.lastLSN}`).join(' ')}] DPT=${JSON.stringify(rec.dirtyPageTable)}`;
  }
  let s = `${base} T${rec.txnId} prev=${rec.prevLSN}`;
  if (rec.type === LogRecordType.UPDATE) {
    s += ` ${rec.pageId}.${rec.key}: ${JSON.stringify(rec.before)} -> ${JSON.stringify(rec.after)}`;
  } else if (rec.type === LogRecordType.CLR) {
    s += ` ${rec.pageId}.${rec.key} <- ${JSON.stringify(rec.after)} (undone=${rec.undoneLSN}, undoNext=${rec.undoNextLSN})`;
  }
  return s;
}

const scenario = buildCrashScenario();

console.log('=== WAL file surviving the crash ===');
for (const rec of WalManager.loadFromFile(scenario.walFile).all()) {
  console.log(' ', fmt(rec));
}
console.log(`checkpoint at LSN ${scenario.checkpointLSN}`);
console.log('disk at crash time:');
for (const [pid, p] of scenario.disk.allPages()) {
  console.log(`  ${pid} pageLSN=${p.pageLSN} data=${JSON.stringify(p.data)}`);
}

// Restart: fresh engine over the durable WAL file + the disk image.
const wal = WalManager.loadFromFile(scenario.walFile);
const engine = new RecoveryEngine(wal, scenario.disk);

console.log('\n=== Phase 1: ANALYSIS ===');
engine.runAnalysis();
console.log('  txnTable:', [...engine.getTxnTable().values()]
  .map((t) => `T${t.txnId}:${t.status}@${t.lastLSN}`).join(' '));
console.log('  dirtyPageTable:', Object.fromEntries(engine.getDirtyPageTable()));

console.log('=== Phase 2: REDO (repeat history) ===');
engine.runRedo();
console.log('  redo starts at LSN', engine.getRedoLSN());

console.log('=== Phase 3: UNDO (losers, descending LSN) ===');
engine.runUndo();
console.log('  CLRs written:', engine.getWrittenCLRs().join(', '));

console.log('\n=== Recovered log tail ===');
for (const rec of wal.all().slice(15)) console.log(' ', fmt(rec));

// Durability barrier (log first, then dirty pages) + protocol close.
engine.complete();

console.log('\n=== Final durable state ===');
for (const [pid, p] of scenario.disk.allPages()) {
  console.log(`  ${pid} pageLSN=${p.pageLSN} data=${JSON.stringify(p.data)}`);
}
console.log('expected:', JSON.stringify(EXPECTED.pages));

const losers = [...engine.getTxnTable().keys()];
console.log(`\nloser transactions still open: ${losers.length === 0 ? 'none' : losers.join(',')}`);
console.log(`latch leaks: ${engine.latchLeakCount()}`);
console.log(`recovery phase: ${engine.getPhase()}`);
