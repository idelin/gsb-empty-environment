/**
 * End-to-end demo:
 *   1. Build the acceptance WAL (5 txns, checkpoint, crash).
 *   2. Run ARIES Analysis / Redo / Undo and print each phase.
 *   3. Run a multi-node concurrent transfer workload through the arbiter
 *      and verify the balance invariant.
 *
 * Run with: npm run demo
 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LogManager } from '../src/log-manager.ts';
import { StorageEngine } from '../src/storage.ts';
import { LockManager, LockAbortedError } from '../src/lock-manager.ts';
import {
  TransactionManager,
  TransactionAbortedError,
} from '../src/transaction.ts';
import { AriesRecovery } from '../src/recovery.ts';
import { buildCrashScenario } from '../src/scenario.ts';
import { formatRecord } from '../src/log-record.ts';
import { mulberry32, randomInt } from '../src/random.ts';

async function recoveryDemo(): Promise<void> {
  console.log('=== ARIES crash recovery demo ===\n');
  const dir = mkdtempSync(join(tmpdir(), 'aries-demo-'));
  const scenario = await buildCrashScenario(dir);
  console.log(`WAL file: ${scenario.walPath}`);
  console.log(
    `committed: T${scenario.committedTxns.t1}, T${scenario.committedTxns.t2}` +
      ` | uncommitted at crash: T${scenario.loserTxns.t3}, T${scenario.loserTxns.t4}, T${scenario.loserTxns.t5}\n`,
  );

  console.log('--- WAL at crash time ---');
  for (const rec of LogManager.readFile(scenario.walPath)) {
    console.log(' ', formatRecord(rec));
  }

  const log = new LogManager(scenario.walPath);
  const storage = new StorageEngine(log, { pagesFile: scenario.pagesPath });
  const report = new AriesRecovery(log, storage).recover(scenario.walPath);

  console.log('\n--- Phase 1: Analysis ---');
  console.log(`  checkpoint LSN: ${report.analysis.checkpointLSN}`);
  console.log(
    `  DirtyPageTable: ${[...report.analysis.dirtyPageTable.entries()]
      .map(([p, l]) => `page ${p} recLSN=${l}`)
      .join(' | ')}`,
  );
  for (const [txnId, entry] of report.analysis.txnTable) {
    console.log(`  TxnTable: T${txnId} ${entry.status} lastLSN=${entry.lastLSN}`);
  }

  console.log('\n--- Phase 2: Redo (repeat history) ---');
  console.log(`  redone LSNs: [${report.redoneLSNs.join(', ')}]`);

  console.log('\n--- Phase 3: Undo (losers, reverse scan + CLRs) ---');
  console.log(`  losers: [${report.undo.losers.map((t) => 'T' + t).join(', ')}]`);
  console.log(`  CLRs written: [${report.undo.clrLSNs.join(', ')}]`);
  console.log(`  ENDs written: [${report.undo.endLSNs.join(', ')}]`);

  console.log('\n--- Durable state after recovery ---');
  for (const [pageId, slots] of Object.entries(storage.diskSnapshot())) {
    console.log(`  page ${pageId}: [${slots.map((s) => `'${s}'`).join(', ')}]`);
  }

  console.log('\n--- WAL tail written by recovery ---');
  const wal = LogManager.readFile(scenario.walPath);
  for (const rec of wal.filter((r) => r.type === 'CLR' || r.type === 'END')) {
    console.log(' ', formatRecord(rec));
  }
}

async function concurrencyDemo(): Promise<void> {
  console.log('\n=== Multi-node concurrency demo (wound-wait arbiter) ===\n');
  const dir = mkdtempSync(join(tmpdir(), 'aries-demo-conc-'));
  const log = new LogManager(join(dir, 'wal.log'), { truncate: true });
  const storage = new StorageEngine(log, { slotsPerPage: 10 });
  const locks = new LockManager();
  const tm = new TransactionManager(log, storage, locks);

  const seed = tm.begin();
  for (let i = 0; i < 10; i++) await tm.write(seed, 0, i, '1000');
  tm.commit(seed);

  const nodes = Array.from({ length: 8 }, (_, nodeId) =>
    (async () => {
      const rand = mulberry32(nodeId + 1);
      for (let k = 0; k < 25; k++) {
        const txnId = tm.begin();
        try {
          const from = randomInt(rand, 0, 9);
          let to = randomInt(rand, 0, 9);
          if (to === from) to = (to + 1) % 10;
          const amount = randomInt(rand, 1, 50);
          const fromBal = Number(await tm.read(txnId, 0, from));
          await tm.write(txnId, 0, from, String(fromBal - amount));
          const toBal = Number(await tm.read(txnId, 0, to));
          await tm.write(txnId, 0, to, String(toBal + amount));
          if (rand() < 0.1) tm.abort(txnId);
          else tm.commit(txnId);
        } catch (err) {
          if (
            err instanceof LockAbortedError ||
            err instanceof TransactionAbortedError
          ) {
            tm.abort(txnId);
          } else {
            throw err;
          }
        }
      }
    })(),
  );
  await Promise.all(nodes);

  const check = tm.begin();
  let total = 0;
  for (let i = 0; i < 10; i++) total += Number(await tm.read(check, 0, i));
  tm.commit(check);

  console.log(`  nodes: 8, txns/node: 25`);
  console.log(`  committed: ${tm.committed.size}, aborted: ${tm.aborted.size}`);
  console.log(`  total balance: ${total} (expected 10000)`);
  console.log(`  wait-for graph acyclic: ${!locks.hasCycle()}`);
  console.log(`  lock manager quiescent (no leaks): ${locks.isQuiescent()}`);
  console.log(
    `  invariant: ${total === 10000 && locks.isQuiescent() ? 'OK' : 'VIOLATED'}`,
  );
}

await recoveryDemo();
await concurrencyDemo();
