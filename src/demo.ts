import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { buildAcceptanceScenario, recoverAcceptanceScenario } from './scenario.ts';
import { verifyRecoveredState } from './verify.ts';
import { reproduce } from './simulator.ts';
import type { LogRecord } from './types.ts';

function fmtRecord(r: LogRecord): string {
  const base = `LSN=${String(r.lsn).padStart(3)} ${r.type.padEnd(17)}`;
  switch (r.type) {
    case 'UPDATE':
    case 'CLR':
      return `${base} T${r.txnId} P${r.pageId}.${r.key} ${r.before}->${r.after}` +
        (r.type === 'CLR' ? ` undoNext=${r.undoNextLSN}` : '');
    case 'END_CHECKPOINT':
      return `${base} begin=${r.beginLSN} TT=[${r.transactionTable.map((e) => 'T' + e.txnId + ':' + e.status).join(', ')}] DPT={${r.dirtyPageTable.map((d) => 'P' + d.pageId + '@' + d.recLSN).join(', ')}}`;
    case 'BEGIN_CHECKPOINT':
      return base;
    default:
      return `${base} T${r.txnId}`;
  }
}

function runScenario(): void {
  const dir = mkdtempSync(join(tmpdir(), 'aries-demo-'));
  const walPath = join(dir, 'wal.jsonl');
  console.log(`=== ARIES acceptance scenario ===`);
  console.log(`WAL file: ${walPath}\n`);

  const scenario = buildAcceptanceScenario(walPath);
  console.log(`Txns: T${scenario.committed.t1}, T${scenario.committed.t2} committed; ` +
    `T${scenario.uncommitted.t3}, T${scenario.uncommitted.t4}, T${scenario.uncommitted.t5} uncommitted`);
  console.log(`Checkpoint taken with T3,T4 active; P1 flushed dirty (steal); crash.\n`);

  const { report, log } = recoverAcceptanceScenario(walPath, scenario.disk);

  console.log(`--- WAL after recovery (${log.durableCount} records) ---`);
  for (const record of log.durable()) console.log('  ' + fmtRecord(record));

  const a = report.analysis;
  console.log(`\n--- Analysis ---`);
  console.log(`  checkpoint begin LSN: ${a.checkpointBeginLSN}`);
  console.log(`  TransactionTable: ${[...a.tt.values()].map((e) => `T${e.txnId}:${e.status}@${e.lastLSN}`).join(', ')}`);
  console.log(`  DirtyPageTable:   ${[...a.dpt.entries()].map(([p, l]) => `P${p}@${l}`).join(', ')}`);
  console.log(`  redoLSN:          ${a.redoLSN}`);
  console.log(`\n--- Redo ---`);
  console.log(`  applied=${report.redoApplied} skipped=${report.redoSkipped}`);
  console.log(`\n--- Undo ---`);
  console.log(`  losers=[${report.losers.map((t) => 'T' + t).join(', ')}] winners=[${report.winners.map((t) => 'T' + t).join(', ')}]`);
  console.log(`  CLRs written=${report.clrsWritten} ENDs written=${report.endsWritten}`);

  console.log(`\n--- Final disk state ---`);
  for (const pageId of scenario.disk.pageIds()) {
    const page = scenario.disk.readPage(pageId);
    console.log(`  P${pageId} (pageLSN=${page.pageLSN}): ${JSON.stringify(page.data)}`);
  }

  const verification = verifyRecoveredState(scenario.disk, log.durable());
  console.log(`\n--- Verification ---`);
  console.log(`  checked ${verification.checkedKeys} keys; winners=[${verification.winners.map((t) => 'T' + t)}] losers=[${verification.losers.map((t) => 'T' + t)}]`);
  if (verification.ok) {
    console.log(`  RESULT: PASS - committed data durable, uncommitted data rolled back.`);
  } else {
    console.log(`  RESULT: FAIL`);
    for (const e of verification.errors) console.log(`    ! ${e}`);
    process.exitCode = 1;
  }
}

function runSimulate(args: string[]): void {
  const seed = Number(args[0] ?? 42);
  const events = Number(args[1] ?? 100_000);
  const crashAt = args[2] !== undefined ? Number(args[2]) : undefined;
  const result = reproduce(seed, events, crashAt);
  console.log(`seed=${result.seed} events=${result.events} crashAt=${result.crashedAt}`);
  console.log(`walLength=${result.walLength} walHash=${result.walHash} diskHash=${result.diskHash}`);
  console.log(`committed=${result.committedTxns} aborted=${result.abortedTxns} losers=[${result.losers.join(',')}]`);
  console.log(`redo applied=${result.recovery.redoApplied} skipped=${result.recovery.redoSkipped} undo CLRs=${result.recovery.clrsWritten}`);
  console.log(`verification: ${result.verification.ok ? 'PASS' : 'FAIL'} (${result.verification.checkedKeys} keys checked)`);
  if (result.leaks.length > 0) {
    console.log(`leaks: ${result.leaks.join('; ')}`);
    process.exitCode = 1;
  }
}

const [, , command, ...rest] = process.argv;
if (command === 'simulate' || command === 'reproduce') {
  runSimulate(rest);
} else {
  runScenario();
}
