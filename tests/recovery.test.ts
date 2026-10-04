import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readFileSync, appendFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/aries/database.ts';
import { RecoveryEngine, type RecoveryReport } from '../src/aries/recovery.ts';
import { DiskStorage } from '../src/aries/storage.ts';
import { readWal } from '../src/aries/wal.ts';
import { RecordType, TxnStatus, ErrorCode } from '../src/aries/types.ts';
import { MetricsRegistry } from '../src/metrics/registry.ts';

interface Scenario {
  dir: string;
  walPath: string;
  storagePath: string;
  metrics: MetricsRegistry;
}

/**
 * Acceptance scenario: 5 transactions, fuzzy checkpoint in the middle,
 * crash afterwards. T1/T2 commit, T3/T4/T5 are losers in flight at crash.
 */
function buildCrashScenario(t: { after: (fn: () => void) => void }): Scenario {
  const dir = mkdtempSync(join(tmpdir(), 'aries-recovery-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const walPath = join(dir, 'wal.log');
  const storagePath = join(dir, 'data.json');
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

  db.crash();
  return { dir, walPath, storagePath, metrics };
}

function diskValues(storagePath: string): Map<string, unknown> {
  const map = new Map<string, unknown>();
  for (const page of new DiskStorage(storagePath).snapshot()) {
    map.set(page.pageId, page.value);
  }
  return map;
}

test('三阶段恢复：回滚未提交事务，持久化已提交事务', (t) => {
  const { walPath, storagePath } = buildCrashScenario(t);
  const metrics = new MetricsRegistry();
  t.after(() => metrics.close());

  const report = new RecoveryEngine(walPath, storagePath, { metrics }).recover();

  // --- Analysis ---
  assert.equal(report.errors.length, 0);
  assert.notEqual(report.analysis.checkpointLSN, null, 'analysis must start from the checkpoint');
  const txnIds = report.analysis.txnTable.map((e) => e.txnId).sort();
  assert.deepEqual(txnIds, [3, 4, 5], 'only loser transactions remain in the TT');
  for (const entry of report.analysis.txnTable) {
    assert.equal(entry.status, TxnStatus.Running);
  }
  const dptPages = report.analysis.dirtyPages.map((e) => e.pageId).sort();
  assert.deepEqual(dptPages, ['P1', 'P2', 'P3', 'P4']);
  assert.notEqual(report.analysis.redoLSN, null);

  // --- Redo ---
  assert.equal(report.redo.applied, 7, 'all seven UPDATE records are replayed');
  assert.equal(report.redo.skippedPageLSN, 0, 'nothing was flushed before the crash');

  // --- Undo ---
  assert.equal(report.undo.clrs, 4, 'T3 wrote 2 updates, T4 and T5 one each');
  assert.deepEqual([...report.undo.endedTxns].sort(), [3, 4, 5]);

  // --- Final durable state ---
  const values = diskValues(storagePath);
  assert.equal(values.get('P1'), 'T1-committed', 'T3 update on P1 rolled back to T1 value');
  assert.equal(values.get('P2'), 'T2-first', 'T4 update on P2 rolled back to T2 value');
  assert.equal(values.get('P3'), 'T2-second', 'T3 update on P3 rolled back to T2 value');
  assert.equal(values.get('P4'), null, 'T5 update on P4 rolled back to empty');

  // --- WAL now contains CLRs and END records for the losers ---
  const { records } = readWal(walPath);
  const clrs = records.filter((r) => r.type === RecordType.Clr);
  assert.equal(clrs.length, 4);
  for (const clr of clrs) {
    assert.ok([3, 4, 5].includes((clr as { txnId: number }).txnId));
  }
  const ends = records.filter((r) => r.type === RecordType.End);
  const endedTxnIds = ends.map((r) => (r as { txnId: number }).txnId);
  for (const loser of [3, 4, 5]) {
    assert.ok(endedTxnIds.includes(loser), `END record written for T${loser}`);
  }

  // --- Metrics captured during recovery ---
  const clrMetric = metrics.findCounter('aries_undo_clr_total');
  assert.equal(clrMetric?.value(), 4);
  const errors = metrics.findCounter('aries_errors_total', { code: ErrorCode.WalCorruptRecord });
  assert.equal(errors, undefined, 'no WAL errors on a clean log');
  const snapshot = metrics.snapshot();
  const phases = snapshot.histograms
    .filter((h) => h.name === 'aries_phase_duration_ns')
    .map((h) => h.labels.phase)
    .sort();
  assert.deepEqual(phases, ['analysis', 'redo', 'undo']);
  for (const h of snapshot.histograms) {
    if (h.name === 'aries_phase_duration_ns') {
      assert.equal(h.count, 1);
      assert.ok(h.sum > 0, 'phase duration recorded in ns');
    }
  }
  const scanned = snapshot.counters
    .filter((c) => c.name === 'aries_log_records_scanned_total')
    .reduce((acc, c) => acc + c.value, 0);
  assert.ok(scanned > 0);
});

test('恢复幂等：重复执行恢复不改变结果', (t) => {
  const { walPath, storagePath } = buildCrashScenario(t);

  const first = new RecoveryEngine(walPath, storagePath).recover();
  const valuesAfterFirst = diskValues(storagePath);

  const second: RecoveryReport = new RecoveryEngine(walPath, storagePath).recover();
  const valuesAfterSecond = diskValues(storagePath);

  assert.equal(second.undo.clrs, 0, 'no losers left after the first recovery');
  assert.equal(second.undo.endedTxns.length, 0);
  assert.deepEqual(valuesAfterSecond, valuesAfterFirst);
  assert.equal(valuesAfterSecond.get('P1'), 'T1-committed');
  assert.equal(valuesAfterSecond.get('P3'), 'T2-second');
  assert.ok(first.undo.clrs > 0);
});

test('Redo 跳过已持久化页面（pageLSN 检查）', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'aries-flush-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const walPath = join(dir, 'wal.log');
  const storagePath = join(dir, 'data.json');

  const db = new Database(walPath, storagePath);
  const t1 = db.begin();
  db.update(t1, 'P1', 'v1');
  db.commit(t1);
  const t2 = db.begin();
  db.update(t2, 'P1', 'v2-uncommitted');
  db.flushPage('P1'); // steal: uncommitted data reaches disk before the crash
  db.crash();

  const report = new RecoveryEngine(walPath, storagePath).recover();
  assert.ok(report.redo.skippedPageLSN >= 1, 'redo skips records already reflected on disk');
  assert.equal(diskValues(storagePath).get('P1'), 'v1', 'undo still restores the committed value');
});

test('WAL 尾部损坏：容错并统计错误码', (t) => {
  const { walPath, storagePath } = buildCrashScenario(t);
  appendFileSync(walPath, '{"crc": 999, "payload": ', 'utf8'); // torn write

  const metrics = new MetricsRegistry();
  t.after(() => metrics.close());
  const report = new RecoveryEngine(walPath, storagePath, { metrics }).recover();

  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].code, ErrorCode.WalCorruptRecord);
  const counter = metrics.findCounter('aries_errors_total', { code: ErrorCode.WalCorruptRecord });
  assert.equal(counter?.value(), 1);
  // Recovery still completes on the valid prefix.
  assert.equal(diskValues(storagePath).get('P1'), 'T1-committed');
});

test('校验和不匹配被检测并记录', (t) => {
  const { walPath, storagePath } = buildCrashScenario(t);
  // Flip a byte inside the last valid line's payload but keep it valid JSON.
  const content = readFileSync(walPath, 'utf8');
  const lines = content.trimEnd().split('\n');
  const last = JSON.parse(lines[lines.length - 1]);
  last.payload.txnId = 4242; // corrupt payload without fixing the crc
  lines[lines.length - 1] = JSON.stringify(last);
  writeFileSync(walPath, lines.join('\n') + '\n', 'utf8');

  const metrics = new MetricsRegistry();
  t.after(() => metrics.close());
  const report = new RecoveryEngine(walPath, storagePath, { metrics }).recover();

  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].code, ErrorCode.WalChecksumMismatch);
  assert.equal(
    metrics.findCounter('aries_errors_total', { code: ErrorCode.WalChecksumMismatch })?.value(),
    1,
  );
});

test('无检查点时执行全量扫描', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'aries-nockpt-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const walPath = join(dir, 'wal.log');
  const storagePath = join(dir, 'data.json');

  const db = new Database(walPath, storagePath);
  const t1 = db.begin();
  db.update(t1, 'P1', 'committed');
  db.commit(t1);
  const t2 = db.begin();
  db.update(t2, 'P1', 'loser');
  db.crash();

  const report = new RecoveryEngine(walPath, storagePath).recover();
  assert.equal(report.analysis.checkpointLSN, null);
  assert.equal(diskValues(storagePath).get('P1'), 'committed');
});
