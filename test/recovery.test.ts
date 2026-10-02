import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { Inspector } from '../src/inspector.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import { buildCrashScenario, SCENARIO_EXPECTED } from '../src/scenario.ts';
import { Storage } from '../src/storage.ts';
import { Wal } from '../src/wal.ts';
import { cleanupDir, makeTempDir } from './helpers.ts';

test('analysis rebuilds txn table and dirty page table from the checkpoint', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const engine = new RecoveryEngine(wal, storage);
    const analysis = engine.analyze(wal.scan());

    assert.equal(analysis.checkpointLSN, SCENARIO_EXPECTED.checkpointLSN);
    // 3 losers in flight; T1/T2 already ENDed and absent from the table.
    assert.deepEqual([...analysis.txnTable.keys()].sort(), SCENARIO_EXPECTED.losers);
    for (const entry of analysis.txnTable.values()) {
      assert.equal(entry.status, 'running');
    }
    assert.equal(analysis.txnTable.get('T3')?.lastLSN, 18);
    assert.equal(analysis.txnTable.get('T4')?.lastLSN, 15);
    assert.equal(analysis.txnTable.get('T5')?.lastLSN, 17);
    assert.deepEqual(
      Object.fromEntries(analysis.dirtyPageTable),
      { P2: 4, P3: 5, P1: 10 },
    );
  } finally {
    await cleanupDir(dir);
  }
});

test('redo repeats history, skipping pages already flushed before the crash', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const engine = new RecoveryEngine(wal, storage);
    const records = wal.scan();
    const analysis = engine.analyze(records);
    const applied = engine.redo(records, analysis.dirtyPageTable);

    // LSN 5 is skipped: P3 was flushed with pageLSN=5 before the checkpoint.
    assert.deepEqual(applied, SCENARIO_EXPECTED.redoApplied);
    assert.equal(storage.getValue('P1', 'a'), 100);
    assert.equal(storage.getValue('P1', 'd'), 7);
    assert.equal(storage.getValue('P2', 'b'), 210);
    assert.equal(storage.getValue('P3', 'c'), 300);
    assert.equal(storage.getPage('P1').pageLSN, 18);
    assert.equal(storage.getPage('P2').pageLSN, 17);
  } finally {
    await cleanupDir(dir);
  }
});

test('undo rolls back losers with CLRs and persists committed data', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const engine = new RecoveryEngine(wal, storage);
    const report = await engine.recover();

    // Committed data persisted.
    assert.equal(storage.getValue('P1', 'a'), 10); // T1 winner
    assert.equal(storage.getValue('P2', 'b'), 21); // T2 winner
    assert.equal(storage.getValue('P3', 'c'), 30); // T1 winner
    // Uncommitted modifications rolled back.
    assert.equal(storage.getValue('P1', 'd'), undefined); // T3 loser, key removed

    const finalData = Object.fromEntries(
      Object.entries(storage.snapshot()).map(([p, s]) => [p, s.data]),
    );
    assert.deepEqual(finalData, SCENARIO_EXPECTED.finalPages);

    // One CLR per loser update (T3 x2, T4 x1, T5 x1), one END per loser.
    assert.equal(report.undo.clrLSNs.length, 4);
    assert.deepEqual([...report.undo.endedTxns].sort(), SCENARIO_EXPECTED.losers);
    assert.equal(report.analysis.txnTable.size, 0, 'no leaked transactions');
    assert.equal(report.analysis.dirtyPageTable.size, 0);

    // CLRs are well formed: undoNextLSN points at the undone update's prevLSN.
    const byLsn = wal.byLsn();
    for (const clrLSN of report.undo.clrLSNs) {
      const clr = byLsn.get(clrLSN);
      assert.equal(clr?.kind, 'CLR');
    }
    const clr19 = byLsn.get(19);
    assert.ok(clr19 && clr19.kind === 'CLR' && clr19.undoNextLSN === 10);

    // Post-recovery formal invariants: strictly no leaks, no skew.
    const inspector = new Inspector();
    const walReport = inspector.inspectWal(wal.scan());
    assert.deepEqual(walReport.violations, []);
    const recoveryReport = inspector.inspectRecovered(wal.scan(), report.analysis.txnTable, storage);
    assert.deepEqual(recoveryReport.violations, []);
    assert.deepEqual(inspector.detectDataSkew(wal.scan(), storage), []);
  } finally {
    await cleanupDir(dir);
  }
});

test('recovery is idempotent: a second pass is a no-op', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    await new RecoveryEngine(wal, storage).recover();
    const sizeAfterFirst = wal.size;

    const report = await new RecoveryEngine(wal, storage).recover();
    assert.deepEqual(report.redoApplied, [], 'all pageLSNs already cover the log');
    assert.deepEqual(report.undo.clrLSNs, [], 'no losers remain');
    assert.equal(wal.size, sizeAfterFirst, 'no new records written');
    assert.equal(storage.getValue('P1', 'a'), 10);
    assert.equal(storage.getValue('P2', 'b'), 21);
    assert.equal(storage.getValue('P3', 'c'), 30);
  } finally {
    await cleanupDir(dir);
  }
});

test('recovery without any checkpoint scans the whole log', async () => {
  const dir = await makeTempDir();
  try {
    const wal = await Wal.create(join(dir, 'wal.log'));
    const storage = await Storage.create(join(dir, 'db.json'), { P1: { x: 0 } });
    await wal.append({ kind: 'BEGIN', txnId: 'T1' });
    await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P1', key: 'x', before: 0, after: 5 });
    await wal.append({ kind: 'COMMIT', txnId: 'T1' });
    await wal.append({ kind: 'BEGIN', txnId: 'T2' });
    await wal.append({ kind: 'UPDATE', txnId: 'T2', pageId: 'P1', key: 'y', before: null, after: 9 });

    const report = await new RecoveryEngine(wal, storage).recover();
    assert.equal(report.analysis.checkpointLSN, null);
    assert.deepEqual(report.redoApplied, [2, 5]);
    assert.equal(storage.getValue('P1', 'x'), 5);
    assert.equal(storage.getValue('P1', 'y'), undefined);
    assert.equal(report.analysis.txnTable.size, 0);
  } finally {
    await cleanupDir(dir);
  }
});

test('undo continues an in-progress abort and sweeps committed txns missing END', async () => {
  const dir = await makeTempDir();
  try {
    const wal = await Wal.create(join(dir, 'wal.log'));
    const storage = await Storage.create(join(dir, 'db.json'), { P1: { a: 1, b: 2 } });
    await wal.append({ kind: 'BEGIN', txnId: 'T1' });
    await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P1', key: 'a', before: 1, after: 100 });
    await wal.append({ kind: 'ABORT', txnId: 'T1' }); // crash mid-abort: no CLR yet
    await wal.append({ kind: 'BEGIN', txnId: 'T2' });
    await wal.append({ kind: 'UPDATE', txnId: 'T2', pageId: 'P1', key: 'b', before: 2, after: 200 });
    await wal.append({ kind: 'COMMIT', txnId: 'T2' }); // crash before END

    const report = await new RecoveryEngine(wal, storage).recover();
    assert.equal(storage.getValue('P1', 'a'), 1, 'aborted txn undone');
    assert.equal(storage.getValue('P1', 'b'), 200, 'committed txn kept');
    assert.deepEqual([...report.undo.endedTxns].sort(), ['T1', 'T2']);
    assert.equal(report.analysis.txnTable.size, 0, 'no leaked transactions');

    const inspector = new Inspector();
    assert.deepEqual(inspector.inspectRecovered(wal.scan(), report.analysis.txnTable, storage).violations, []);
  } finally {
    await cleanupDir(dir);
  }
});
