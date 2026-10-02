import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { Inspector } from '../src/inspector.ts';
import type { LogRecord } from '../src/log-record.ts';
import { buildCrashScenario } from '../src/scenario.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import { Storage } from '../src/storage.ts';
import { cleanupDir, makeTempDir } from './helpers.ts';

const inspector = new Inspector();

function invariants(records: LogRecord[]): string[] {
  return inspector.inspectWal(records).violations.map((v) => v.invariant);
}

function rec(partial: Record<string, unknown>): LogRecord {
  return { prevLSN: null, txnId: 'T1', ...partial } as LogRecord;
}

test('empty WAL is trivially valid', () => {
  const report = inspector.inspectWal([]);
  assert.equal(report.ok, true);
  assert.equal(report.checkedRecords, 0);
  assert.deepEqual(report.violations, []);
});

test('healthy WAL after full recovery has zero violations', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    await new RecoveryEngine(wal, storage).recover();
    const report = inspector.inspectWal(wal.scan());
    assert.equal(report.ok, true);
    assert.equal(report.checkedRecords, 25);
  } finally {
    await cleanupDir(dir);
  }
});

test('boundary: duplicate and non-contiguous LSNs are flagged', () => {
  const dup = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'BEGIN', lsn: 1, txnId: 'T2' }),
  ]);
  assert.ok(dup.includes('LSN_UNIQUE'));
  assert.ok(dup.includes('LSN_CONTIGUITY'));

  const gap = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'COMMIT', lsn: 3, prevLSN: 1 }),
  ]);
  assert.ok(gap.includes('LSN_CONTIGUITY'));
});

test('dangling references: prevLSN pointing nowhere', () => {
  const found = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'UPDATE', lsn: 2, prevLSN: 99, pageId: 'P1', key: 'a', before: 0, after: 1 }),
  ]);
  assert.ok(found.includes('PREV_LSN_DANGLING'));
});

test('dangling references: prevLSN order and cross-transaction links', () => {
  const backwards = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'UPDATE', lsn: 2, prevLSN: 1, pageId: 'P1', key: 'a', before: 0, after: 1 }),
    rec({ kind: 'UPDATE', lsn: 3, prevLSN: 3, pageId: 'P1', key: 'a', before: 1, after: 2 }),
  ]);
  assert.ok(backwards.includes('PREV_LSN_ORDER'));

  const cross = invariants([
    rec({ kind: 'BEGIN', lsn: 1, txnId: 'T1' }),
    rec({ kind: 'BEGIN', lsn: 2, txnId: 'T2' }),
    rec({ kind: 'UPDATE', lsn: 3, txnId: 'T2', prevLSN: 1, pageId: 'P1', key: 'a', before: 0, after: 1 }),
  ]);
  assert.ok(cross.includes('PREV_LSN_CROSS_TXN'));
});

test('orphan loops: cycles in transaction chains are detected', () => {
  const found = invariants([
    rec({ kind: 'BEGIN', lsn: 1, prevLSN: 3 }),
    rec({ kind: 'UPDATE', lsn: 2, prevLSN: 1, pageId: 'P1', key: 'a', before: 0, after: 1 }),
    rec({ kind: 'UPDATE', lsn: 3, prevLSN: 2, pageId: 'P1', key: 'a', before: 1, after: 2 }),
  ]);
  assert.ok(found.includes('ORPHAN_LOOP'));
});

test('orphan records: activity without BEGIN', () => {
  const found = invariants([
    rec({ kind: 'UPDATE', lsn: 1, pageId: 'P1', key: 'a', before: 0, after: 1 }),
  ]);
  assert.ok(found.includes('ORPHAN_RECORD'));
});

test('lifecycle: record after END, END without terminal, double terminal, double BEGIN', () => {
  const afterEnd = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'COMMIT', lsn: 2, prevLSN: 1 }),
    rec({ kind: 'END', lsn: 3, prevLSN: 2 }),
    rec({ kind: 'UPDATE', lsn: 4, prevLSN: 3, pageId: 'P1', key: 'a', before: 0, after: 1 }),
  ]);
  assert.ok(afterEnd.includes('RECORD_AFTER_END'));

  const bareEnd = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'END', lsn: 2, prevLSN: 1 }),
  ]);
  assert.ok(bareEnd.includes('END_WITHOUT_TERMINAL'));

  const doubleTerminal = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'COMMIT', lsn: 2, prevLSN: 1 }),
    rec({ kind: 'ABORT', lsn: 3, prevLSN: 2 }),
  ]);
  assert.ok(doubleTerminal.includes('TXN_DOUBLE_TERMINAL'));

  const doubleBegin = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'BEGIN', lsn: 2 }),
  ]);
  assert.ok(doubleBegin.includes('TXN_SINGLE_BEGIN'));
});

test('CLR undoNextLSN integrity: dangling, out-of-order, cross-transaction', () => {
  const dangling = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'CLR', lsn: 2, prevLSN: 1, pageId: 'P1', key: 'a', before: 1, after: 0, undoNextLSN: 42 }),
  ]);
  assert.ok(dangling.includes('CLR_UNDO_NEXT_DANGLING'));

  const forward = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({ kind: 'CLR', lsn: 2, prevLSN: 1, pageId: 'P1', key: 'a', before: 1, after: 0, undoNextLSN: 3 }),
    rec({ kind: 'END', lsn: 3, prevLSN: 2 }),
  ]);
  assert.ok(forward.includes('CLR_UNDO_NEXT_ORDER'));

  const cross = invariants([
    rec({ kind: 'BEGIN', lsn: 1, txnId: 'T1' }),
    rec({ kind: 'BEGIN', lsn: 2, txnId: 'T2' }),
    rec({ kind: 'CLR', lsn: 3, txnId: 'T2', prevLSN: 2, pageId: 'P1', key: 'a', before: 1, after: 0, undoNextLSN: 1 }),
  ]);
  assert.ok(cross.includes('CLR_UNDO_NEXT_CROSS_TXN'));
});

test('checkpoint references must point at existing earlier LSNs', () => {
  const found = invariants([
    rec({ kind: 'BEGIN', lsn: 1 }),
    rec({
      kind: 'CHECKPOINT', lsn: 2, txnId: null,
      txnTable: [{ txnId: 'T1', status: 'running', lastLSN: 77 }],
      dirtyPageTable: [{ pageId: 'P1', recLSN: 2 }],
    }),
  ]);
  assert.equal(found.filter((i) => i === 'CHECKPOINT_REFS_VALID').length, 2);
});

test('data skew: missing, diverged and unexpected residue are classified', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    await new RecoveryEngine(wal, storage).recover();
    const records = wal.scan();

    // No skew on the healthy state.
    assert.deepEqual(inspector.detectDataSkew(records, storage), []);

    // diverged: committed value corrupted on disk
    storage.setValue('P1', 'a', 999, 24);
    // missing: committed value deleted from disk
    storage.setValue('P2', 'b', null, 20);
    // unexpected: uncommitted residue reappears
    storage.setValue('P1', 'd', 7, 24);

    const skew = inspector.detectDataSkew(records, storage);
    const byKind = new Map(skew.map((s) => [s.kind, s]));
    assert.equal(skew.length, 3);
    assert.deepEqual(byKind.get('diverged'), { pageId: 'P1', key: 'a', expected: 10, actual: 999, kind: 'diverged' });
    assert.deepEqual(byKind.get('missing'), { pageId: 'P2', key: 'b', expected: 21, actual: undefined, kind: 'missing' });
    assert.deepEqual(byKind.get('unexpected'), { pageId: 'P1', key: 'd', expected: null, actual: 7, kind: 'unexpected' });

    const report = inspector.inspectRecovered(records, new Map(), storage);
    assert.equal(report.violations.filter((v) => v.invariant === 'NO_DATA_SKEW').length, 3);
  } finally {
    await cleanupDir(dir);
  }
});

test('post-recovery invariants catch leaks and uncompensated losers', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const engine = new RecoveryEngine(wal, storage);
    const records = wal.scan();
    const analysis = engine.analyze(records);
    engine.redo(records, analysis.dirtyPageTable);
    // Skip undo entirely: losers stay in the table, updates uncompensated.
    const report = inspector.inspectRecovered(records, analysis.txnTable, storage);
    const names = report.violations.map((v) => v.invariant);
    assert.ok(names.includes('TXN_TABLE_EMPTY'), 'leaked transaction table detected');
    assert.ok(names.includes('LOSER_UPDATE_COMPENSATED'), 'missing CLR detected');
    assert.ok(names.includes('LOSER_TXN_ENDED'), 'missing END detected');
    assert.ok(names.includes('NO_DATA_SKEW'), 'loser residue detected');
    assert.equal(report.ok, false);
  } finally {
    await cleanupDir(dir);
  }
});

test('post-recovery invariants: CLR on a committed txn and pageLSN drift', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const report = await new RecoveryEngine(wal, storage).recover();

    // Forge a CLR belonging to committed txn T1.
    await wal.append({ kind: 'CLR', txnId: 'T1', pageId: 'P1', key: 'a', before: 10, after: 0, undoNextLSN: null });
    const forged = inspector.inspectRecovered(wal.scan(), report.analysis.txnTable, storage);
    assert.ok(forged.violations.some((v) => v.invariant === 'COMMITTED_TXN_NO_CLR'));
    // The forged CLR also perturbs pageLSN expectations.
    assert.ok(forged.violations.some((v) => v.invariant === 'PAGE_LSN_CONSISTENT'));
  } finally {
    await cleanupDir(dir);
  }
});

test('inspector is pure: repeated inspections do not mutate input', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const records = wal.scan();
    const before = JSON.stringify(records);
    inspector.inspectWal(records);
    inspector.detectDataSkew(records, storage);
    assert.equal(JSON.stringify(records), before);
  } finally {
    await cleanupDir(dir);
  }
});
