/**
 * End-to-end demo:
 *  1. Build a WAL with 5 transactions (2 committed, 3 in-flight), checkpoint,
 *     then "crash".
 *  2. Run ARIES recovery (Analysis -> Redo -> Undo) and print the report.
 *  3. Show the multi-tenant DRR scheduler dispatching weighted tenants.
 */

import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { LogManager } from '../src/wal.ts';
import { PageStore } from '../src/storage.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { TenantScheduler } from '../src/scheduler.ts';

const dir = await mkdtemp(join(tmpdir(), 'aries-demo-'));
const walPath = join(dir, 'wal.log');
const pagePath = join(dir, 'pages.jsonl');

console.log('=== Phase 0: run workload, checkpoint, then CRASH ===');
const wal = new LogManager(walPath);
const rec = (r: Parameters<LogManager['append']>[0]) => {
  const full = wal.append(r);
  console.log(`  LSN ${String(full.lsn).padStart(2)}  ${full.type.padEnd(10)} ${JSON.stringify(r)}`);
  return full;
};
rec({ type: 'update', txnId: 1, pageId: 'P1', prevLSN: 0, before: 'init1', after: 'T1-data' });
rec({ type: 'update', txnId: 2, pageId: 'P2', prevLSN: 0, before: 'init2', after: 'T2-data' });
rec({ type: 'commit', txnId: 1, prevLSN: 1 });
rec({ type: 'end', txnId: 1, prevLSN: 3 });
rec({ type: 'update', txnId: 3, pageId: 'P1', prevLSN: 0, before: 'T1-data', after: 'T3-a' });
rec({ type: 'commit', txnId: 2, prevLSN: 2 });
rec({ type: 'end', txnId: 2, prevLSN: 6 });
rec({
  type: 'checkpoint',
  transactions: { '3': { status: 'running', lastLSN: 5 } },
  dirtyPages: { P1: 1, P2: 2 },
});
rec({ type: 'update', txnId: 4, pageId: 'P3', prevLSN: 0, before: 'init3', after: 'T4-data' });
rec({ type: 'update', txnId: 5, pageId: 'P2', prevLSN: 0, before: 'T2-data', after: 'T5-a' });
rec({ type: 'update', txnId: 3, pageId: 'P4', prevLSN: 5, before: 'init4', after: 'T3-b' });
rec({ type: 'update', txnId: 4, pageId: 'P1', prevLSN: 9, before: 'T3-a', after: 'T4-x' });
await wal.flush();
const pages = new PageStore(pagePath);
pages.apply('P2', 'T2-data', 2); // only this page reached disk pre-crash
await pages.flush();
console.log('  *** CRASH *** (only P2 was durable; T3/T4/T5 never committed)\n');

console.log('=== Phase 1-3: ARIES recovery ===');
const store = new PageStore(pagePath);
const report = await new RecoveryManager(walPath, store).recover();
console.log('  Analysis: checkpoint @LSN', report.analysis.checkpointLSN);
console.log('    losers       =', report.analysis.losers);
console.log('    dirtyPages   =', Object.fromEntries(report.analysis.dirtyPageTable));
console.log('  Redo   : applied LSNs', report.redoneLSNs, '(LSN 2 skipped: already durable)');
console.log('  Undo   : undone LSNs', report.undoneLSNs, '| CLRs written:', report.clrLSNs);
await store.load();
console.log('  Final pages  :', Object.fromEntries(store.all().map((p) => [p.pageId, p.data])));
console.log('  -> committed T1/T2 data persisted; uncommitted T3/T4/T5 rolled back\n');

console.log('=== Multi-tenant DRR scheduler ===');
const scheduler = new TenantScheduler([
  { id: 'analytics', weight: 3, maxConcurrent: 100 },
  { id: 'oltp', weight: 1, maxConcurrent: 100 },
]);
const order: string[] = [];
const jobs: Promise<void>[] = [];
for (let i = 0; i < 6; i++) {
  jobs.push(scheduler.submit('analytics', async () => void order.push('analytics')));
  jobs.push(scheduler.submit('oltp', async () => void order.push('oltp')));
}
await Promise.all(jobs);
console.log('  dispatch order (weights 3:1):', order.join(' '));
console.log('  stats:', { analytics: scheduler.stats('analytics'), oltp: scheduler.stats('oltp') });

await rm(dir, { recursive: true, force: true });
