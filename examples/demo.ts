/**
 * End-to-end demo: build a WAL with 2 committed + 3 in-flight transactions,
 * checkpoint, crash, then run ARIES Analysis/Redo/Undo and export metrics.
 *
 * Run: node --experimental-strip-types examples/demo.ts
 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/aries/database.ts';
import { RecoveryEngine } from '../src/aries/recovery.ts';
import { DiskStorage } from '../src/aries/storage.ts';
import { MetricsRegistry } from '../src/metrics/registry.ts';
import { Aggregator } from '../src/metrics/aggregator.ts';
import { toJSON, toPrometheus } from '../src/metrics/exporter.ts';

const dir = mkdtempSync(join(tmpdir(), 'aries-demo-'));
const walPath = join(dir, 'wal.log');
const storagePath = join(dir, 'data.json');

console.log(`workspace: ${dir}\n`);

// ---- Normal processing -------------------------------------------------
const metrics = new MetricsRegistry();
const db = new Database(walPath, storagePath, { metrics });

const t1 = db.begin();
db.update(t1, 'P1', 'T1-committed');
db.commit(t1);

const t2 = db.begin();
db.update(t2, 'P2', 'T2-first');

const t3 = db.begin();
db.update(t3, 'P1', 'T3-dirty');

db.checkpoint();

db.update(t2, 'P3', 'T2-second');
db.commit(t2);

const t4 = db.begin();
db.update(t4, 'P2', 'T4-dirty');

const t5 = db.begin();
db.update(t5, 'P4', 'T5-dirty');
db.update(t3, 'P3', 'T3-dirty');

console.log('*** CRASH *** (buffer pool discarded, dirty pages lost)\n');
db.crash();

// ---- Recovery ------------------------------------------------------------
const report = new RecoveryEngine(walPath, storagePath, { metrics }).recover();

console.log('Analysis:');
console.log(`  checkpoint LSN : ${report.analysis.checkpointLSN}`);
console.log(`  redo starts at : ${report.analysis.redoLSN}`);
console.log(`  txn table      : ${JSON.stringify(report.analysis.txnTable)}`);
console.log(`  dirty pages    : ${JSON.stringify(report.analysis.dirtyPages)}`);
console.log('Redo:');
console.log(`  applied=${report.redo.applied} skipped(pageLSN)=${report.redo.skippedPageLSN}`);
console.log('Undo:');
console.log(`  CLRs written=${report.undo.clrs} ended txns=${report.undo.endedTxns.join(',')}`);

console.log('\nDurable state after recovery:');
for (const page of new DiskStorage(storagePath).snapshot()) {
  console.log(`  ${page.pageId} = ${JSON.stringify(page.value)} (pageLSN=${page.pageLSN})`);
}

// ---- Metrics -------------------------------------------------------------
const agg = new Aggregator(metrics);
console.log('\nPhase latency stats (ns):');
for (const h of metrics.allHistograms()) {
  if (h.name !== 'aries_phase_duration_ns') continue;
  const stats = agg.histogramStats(h);
  console.log(`  ${h.labels.phase.padEnd(8)} count=${stats.count} mean=${Math.round(stats.mean ?? 0)}`);
}
const hitRate = agg.hitRate('buffer_pool', 'buffer_pool_hits_total', 'buffer_pool_misses_total');
console.log(`\nBuffer pool: hits=${hitRate.hits} misses=${hitRate.misses} hitRate=${hitRate.hitRate}`);

console.log('\n--- JSON export (excerpt) ---');
const json = JSON.parse(toJSON(metrics));
console.log(JSON.stringify({ counters: json.counters, gauges: json.gauges }, null, 2));

console.log('--- Prometheus export (excerpt) ---');
console.log(
  toPrometheus(metrics)
    .split('\n')
    .filter((l) => l.includes('aries_undo_clr_total') || l.includes('phase_duration'))
    .join('\n'),
);
