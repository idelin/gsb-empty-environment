import test from 'node:test';
import assert from 'node:assert/strict';
import { HeaderStore } from '../src/index.ts';
import { fixtureState } from '../fixtures/header.fixture.ts';
import { assertSnapshotInvariants } from './helpers.ts';

test('hiding every leaf of a group removes the group, leaving no invalid span', () => {
  const store = new HeaderStore(fixtureState());
  store.commit([{ type: 'hide', columnIds: ['n1', 'n2'] }]);
  const snap = store.snapshot;
  assertSnapshotInvariants(snap);

  assert.ok(!snap.cells.some((c) => c.id === 'g:nested'), 'empty group gone');
  assert.equal(snap.rows, 2); // one header level collapses away
  assert.equal(snap.columnCount, 5);
  assert.deepEqual(
    snap.visibleColumns.map((vc) => vc.columnId),
    ['alpha', 'beta', 'm1', 'm2', 'solo'],
  );
  // metrics group shrinks to its remaining leaves.
  const metrics = snap.cells.find((c) => c.id === 'g:metrics')!;
  assert.equal(metrics.colSpan, 2);
});

test('hiding a whole tree leaves an empty, consistent header', () => {
  const store = new HeaderStore(fixtureState());
  store.commit([
    { type: 'hide', columnIds: ['alpha', 'beta', 'm1', 'm2', 'n1', 'n2', 'solo'] },
  ]);
  const snap = store.snapshot;
  assertSnapshotInvariants(snap);
  assert.equal(snap.rows, 0);
  assert.equal(snap.columnCount, 0);
  assert.deepEqual(snap.visibleColumns, []);
});

test('collapsed group renders as one column with full rowspan and keeps coverage', () => {
  const store = new HeaderStore(fixtureState());
  store.commit([{ type: 'collapse', groupId: 'metrics' }]);
  const snap = store.snapshot;
  assertSnapshotInvariants(snap);

  const metrics = snap.cells.find((c) => c.id === 'g:metrics')!;
  assert.equal(metrics.collapsed, true);
  assert.equal(metrics.colSpan, 1);
  assert.equal(metrics.rowSpan, snap.rows); // stretches to the bottom
  assert.deepEqual(metrics.coveredColumnIds, ['m1', 'm2', 'n1', 'n2']);

  // One data column for the whole group; representative is the first leaf.
  const vc = snap.visibleColumns.find((v) => v.collapsedGroupId === 'metrics')!;
  assert.equal(vc.columnId, 'm1');
  assert.equal(vc.ownerGroupId, 'metrics');
  assert.deepEqual(vc.coveredColumnIds, ['m1', 'm2', 'n1', 'n2']);
  // Every covered column maps to the same data index.
  for (const id of ['m1', 'm2', 'n1', 'n2']) {
    assert.equal(snap.dataIndexByColumnId.get(id), vc.dataIndex);
  }
  assert.equal(snap.columnCount, 4); // alpha, beta, metrics, solo
});

test('collapsed group whose leaves are all hidden disappears entirely', () => {
  const store = new HeaderStore(fixtureState());
  store.commit([
    { type: 'collapse', groupId: 'nested' },
    { type: 'hide', columnIds: ['n1', 'n2'] },
  ]);
  const snap = store.snapshot;
  assertSnapshotInvariants(snap);
  assert.ok(!snap.cells.some((c) => c.id === 'g:nested'));
  assert.ok(!snap.visibleColumns.some((vc) => vc.collapsedGroupId === 'nested'));
});

test('cross-group move keeps explicit ownership and consistent indices', () => {
  const store = new HeaderStore(fixtureState());
  store.commit([
    { type: 'moveColumn', columnId: 'solo', toGroupId: 'nested', index: 1 },
  ]);
  const snap = store.snapshot;
  assertSnapshotInvariants(snap);

  assert.deepEqual(
    snap.visibleColumns.map((vc) => vc.columnId),
    ['alpha', 'beta', 'm1', 'm2', 'n1', 'solo', 'n2'],
  );
  const solo = snap.visibleColumns.find((vc) => vc.columnId === 'solo')!;
  assert.equal(solo.ownerGroupId, 'nested'); // explicit new owner
  assert.equal(solo.dataIndex, 5);
  const nested = snap.cells.find((c) => c.id === 'g:nested')!;
  assert.equal(nested.colSpan, 3);
  // Old top-level slot is gone; total width unchanged.
  assert.equal(snap.columnCount, 7);
});

test('same-label columns with different ids are independent', () => {
  const store = new HeaderStore(fixtureState());
  // Both columns are labelled "Score"; hiding by id must hit only one.
  store.commit([{ type: 'hide', columnIds: ['m1'] }]);
  const snap = store.snapshot;
  assertSnapshotInvariants(snap);

  assert.deepEqual(
    snap.visibleColumns.map((vc) => vc.columnId),
    ['alpha', 'beta', 'm2', 'n1', 'n2', 'solo'],
  );
  const m2 = snap.cells.find((c) => c.id === 'c:m2')!;
  assert.equal(m2.label, 'Score');
  assert.ok(!snap.cells.some((c) => c.id === 'c:m1'));
  assert.equal(snap.dataIndexByColumnId.get('m1'), undefined);
  assert.equal(snap.dataIndexByColumnId.get('m2'), 2);

  // Moving the other "Score" across groups does not disturb the hidden one.
  store.commit([{ type: 'moveColumn', columnId: 'm2', toGroupId: 'basic', index: 0 }]);
  const snap2 = store.snapshot;
  assertSnapshotInvariants(snap2);
  assert.deepEqual(
    snap2.visibleColumns.map((vc) => vc.columnId),
    ['m2', 'alpha', 'beta', 'n1', 'n2', 'solo'],
  );
  assert.equal(snap2.visibleColumns[0].ownerGroupId, 'basic');
});
