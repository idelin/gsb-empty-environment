/** End-to-end demo: build the crash scenario, run ARIES recovery, inspect. */

import { rm } from 'node:fs/promises';
import { join } from 'node:path';
import { formatRecord } from './log-record.ts';
import { Inspector } from './inspector.ts';
import { RecoveryEngine } from './recovery.ts';
import { buildCrashScenario, SCENARIO_EXPECTED } from './scenario.ts';

const dir = join(process.cwd(), 'demo-data');
await rm(dir, { recursive: true, force: true });

console.log('=== ARIES crash-recovery demo ===');
console.log(`data dir: ${dir}\n`);

const { wal, storage } = await buildCrashScenario(dir);

console.log('--- WAL at crash (2 committed txns T1/T2, 3 in-flight txns T3/T4/T5) ---');
for (const r of wal.scan()) console.log('  ' + formatRecord(r));
console.log('\n--- disk image at crash (P2 was never flushed) ---');
printStorage();

const engine = new RecoveryEngine(wal, storage);
const records = wal.scan();

console.log('\n=== phase 1: ANALYSIS ===');
const analysis = engine.analyze(records);
console.log(`  last checkpoint LSN: ${analysis.checkpointLSN}`);
console.log('  transaction table:');
for (const e of analysis.txnTable.values()) {
  console.log(`    ${e.txnId} status=${e.status} lastLSN=${e.lastLSN}`);
}
console.log('  dirty page table:');
for (const [pageId, recLSN] of analysis.dirtyPageTable) {
  console.log(`    ${pageId} recLSN=${recLSN}`);
}

console.log('\n=== phase 2: REDO (repeat history) ===');
const redoApplied = engine.redo(records, analysis.dirtyPageTable);
console.log(`  reapplied LSNs: [${redoApplied.join(', ')}]`);
printStorage();

console.log('\n=== phase 3: UNDO (losers, reverse scan with CLRs) ===');
const undo = await engine.undo(records, analysis.txnTable);
analysis.dirtyPageTable.clear();
await storage.flush();
console.log(`  CLRs written: [${undo.clrLSNs.join(', ')}]`);
console.log(`  transactions ended: [${undo.endedTxns.join(', ')}]`);
console.log('  new WAL tail:');
for (const r of wal.scan().slice(records.length)) console.log('    ' + formatRecord(r));

console.log('\n--- final disk image (committed persisted, uncommitted rolled back) ---');
printStorage();

console.log('\n=== formal invariant inspection ===');
const inspector = new Inspector();
const walReport = inspector.inspectWal(wal.scan());
const recoveryReport = inspector.inspectRecovered(wal.scan(), analysis.txnTable, storage);
const skew = inspector.detectDataSkew(wal.scan(), storage);
console.log(`  WAL integrity:      ${walReport.ok ? 'OK' : 'VIOLATIONS'} (${walReport.checkedRecords} records checked)`);
console.log(`  recovery invariants:${recoveryReport.ok ? ' OK' : ' VIOLATIONS'} (incl. no leaked transactions)`);
console.log(`  data skew:          ${skew.length === 0 ? 'none' : JSON.stringify(skew)}`);
for (const v of [...walReport.violations, ...recoveryReport.violations]) {
  console.log(`    [${v.invariant}] ${v.detail}`);
}

const expected = SCENARIO_EXPECTED.finalPages;
const actual = Object.fromEntries(
  Object.entries(storage.snapshot()).map(([p, s]) => [p, s.data]),
);
const pass = JSON.stringify(actual) === JSON.stringify(expected);
console.log(`\nacceptance: final disk ${pass ? 'MATCHES' : 'DIFFERS FROM'} expected committed-only state`);
console.log(`  expected: ${JSON.stringify(expected)}`);
console.log(`  actual:   ${JSON.stringify(actual)}`);
if (!pass || !walReport.ok || !recoveryReport.ok) process.exit(1);

function printStorage(): void {
  for (const [pageId, state] of Object.entries(storage.snapshot())) {
    console.log(`    ${pageId} pageLSN=${state.pageLSN} data=${JSON.stringify(state.data)}`);
  }
}
