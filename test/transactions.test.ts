import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HeaderStore } from '../src/ops.ts';
import { rebuildFromLeafOrder, diffSnapshots, checkInvariants } from '../src/reference.ts';
import { glyphMetrics, sampleDoc } from '../src/fixtures.ts';
import type { HeaderSnapshot } from '../src/types.ts';

function published(snapshot: HeaderSnapshot): Omit<HeaderSnapshot, 'version'> {
  const { version, ...rest } = snapshot;
  return rest;
}

function assertConsistent(store: HeaderStore): void {
  const reference = rebuildFromLeafOrder(store.doc, glyphMetrics, store.version);
  assert.deepEqual(diffSnapshots(store.snapshot, reference), []);
  assert.deepEqual(checkInvariants(store.snapshot), []);
}

test('a transaction publishes exactly one consistent snapshot', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const snap = store.commit([
    { type: 'hide', leafId: 'c-age' },
    { type: 'move', leafId: 'c-math', targetGroupId: 'g-basic', index: 1 },
    { type: 'setCollapsed', groupId: 'g-scores', collapsed: true },
  ]);
  assert.equal(snap.version, 1);
  assert.equal(store.version, 1);
  assertConsistent(store);
  // all three effects visible at once
  assert.deepEqual(
    snap.columns.map((c) => c.key),
    ['leaf:c-name', 'leaf:c-math', 'group:g-scores', 'leaf:c-rank', 'leaf:c-note'],
  );
});

test('cross-group move keeps explicit ownership and shrinks the old group', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const snap = store.commit([{ type: 'move', leafId: 'c-math', targetGroupId: 'g-basic', index: 1 }]);
  assert.deepEqual(snap.leafOwner['c-math'], ['g-basic']);
  assert.deepEqual(snap.leafOwner['c-eng'], ['g-metrics', 'g-scores']);
  const basic = snap.cells.find((c) => c.key === 'group:g-basic')!;
  const scores = snap.cells.find((c) => c.key === 'group:g-scores')!;
  assert.equal(basic.colspan, 3);
  assert.equal(scores.colspan, 1);
  // moved leaf now sits at the shallower level with a taller rowspan
  const math = snap.cells.find((c) => c.key === 'leaf:c-math')!;
  assert.deepEqual([math.row, math.rowspan], [1, 2]);
  assertConsistent(store);
});

test('moving every leaf out of a group makes the group disappear', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const snap = store.commit([
    { type: 'move', leafId: 'c-math', targetGroupId: 'g-basic', index: 2 },
    { type: 'move', leafId: 'c-eng', targetGroupId: 'g-basic', index: 3 },
  ]);
  assert.ok(!snap.cells.some((c) => c.key === 'group:g-scores'));
  const metrics = snap.cells.find((c) => c.key === 'group:g-metrics')!;
  assert.equal(metrics.colspan, 1);
  assertConsistent(store);
});

test('transactions are reversible: undo restores the exact published state', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  store.commit([
    { type: 'hide', leafId: 'c-age' },
    { type: 'setCollapsed', groupId: 'g-metrics', collapsed: true },
  ]);
  store.commit([
    { type: 'move', leafId: 'c-note', targetGroupId: 'g-scores', index: 0 },
    { type: 'hide', leafId: 'c-name' },
  ]);
  store.undo();
  store.undo();
  assert.deepEqual(published(store.snapshot), published(v0));
  assertConsistent(store);
});

test('undoing a mid-sequence transaction composes with later ones', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  store.commit([{ type: 'hide', leafId: 'c-age' }]);
  const afterHide = store.snapshot;
  store.commit([{ type: 'move', leafId: 'c-rank', targetGroupId: 'g-basic', index: 0 }]);
  store.undo();
  assert.deepEqual(published(store.snapshot), published(afterHide));
  assertConsistent(store);
});

test('invalid ops are rejected before any state changes', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  assert.throws(() => store.commit([{ type: 'hide', leafId: 'nope' }]), /unknown leaf/);
  assert.throws(
    () => store.commit([{ type: 'setCollapsed', groupId: 'root', collapsed: true }]),
    /root/,
  );
  assert.equal(store.version, 0);
  assert.equal(store.snapshot.columns.length, 6);
});
