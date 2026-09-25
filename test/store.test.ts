import test from 'node:test';
import assert from 'node:assert/strict';
import { HeaderStore, type HeaderSnapshot } from '../src/index.ts';
import { fixtureState } from '../fixtures/header.fixture.ts';
import { assertSnapshotInvariants } from './helpers.ts';

test('a commit publishes spans, keyboard tree and data mapping atomically', () => {
  const store = new HeaderStore(fixtureState());
  const published: HeaderSnapshot[] = [];
  store.subscribe((snap) => published.push(snap));

  store.commit([
    { type: 'hide', columnIds: ['n1'] },
    { type: 'collapse', groupId: 'metrics' },
    { type: 'moveColumn', columnId: 'solo', toGroupId: 'basic', index: 0 },
  ]);

  // Exactly one notification for the whole batch.
  assert.equal(published.length, 1);
  const snap = published[0];
  assertSnapshotInvariants(snap);

  // All three effects are visible together in the single snapshot.
  assert.deepEqual(
    snap.visibleColumns.map((vc) => vc.columnId),
    ['solo', 'alpha', 'beta', 'm1'],
  );
  const metrics = snap.cells.find((c) => c.id === 'g:metrics')!;
  assert.equal(metrics.collapsed, true);
  assert.deepEqual(metrics.coveredColumnIds, ['m1', 'm2', 'n2']); // n1 hidden
  assert.equal(snap.visibleColumns[0].ownerGroupId, 'basic');
  assert.ok(snap.keyboard['g:metrics']);
});

test('undo/redo compose arbitrary operation sequences reversibly', () => {
  const store = new HeaderStore(fixtureState());
  const initial = store.snapshot;

  store.commit([{ type: 'hide', columnIds: ['alpha'] }]);
  store.commit([{ type: 'collapse', groupId: 'nested' }]);
  store.commit([{ type: 'moveColumn', columnId: 'beta', toGroupId: 'nested', index: 0 }]);
  const afterThree = store.snapshot;
  assert.equal(store.version, 3);

  assert.ok(store.undo());
  assert.ok(store.undo());
  assert.ok(store.undo());
  assert.equal(store.version, 0);
  assert.deepEqual(store.snapshot.visibleColumns, initial.visibleColumns);
  assert.deepEqual(store.snapshot.cells, initial.cells);
  assert.equal(store.undo(), null); // bottom of history

  assert.ok(store.redo());
  assert.ok(store.redo());
  assert.ok(store.redo());
  assert.deepEqual(store.snapshot.cells, afterThree.cells);
  assert.deepEqual(store.snapshot.keyboard, afterThree.keyboard);
  assert.equal(store.redo(), null); // top of history
});

test('a new commit after undo drops the redo tail', () => {
  const store = new HeaderStore(fixtureState());
  store.commit([{ type: 'hide', columnIds: ['alpha'] }]);
  store.undo();
  store.commit([{ type: 'hide', columnIds: ['beta'] }]);
  assert.equal(store.redo(), null);
  assert.deepEqual(
    store.snapshot.visibleColumns.map((vc) => vc.columnId),
    ['alpha', 'm1', 'm2', 'n1', 'n2', 'solo'],
  );
});

test('invalid ops reject the whole transaction and publish nothing', () => {
  const store = new HeaderStore(fixtureState());
  let notified = 0;
  store.subscribe(() => notified++);
  assert.throws(() =>
    store.commit([
      { type: 'hide', columnIds: ['alpha'] },
      { type: 'hide', columnIds: ['does-not-exist'] },
    ]),
  );
  assert.equal(notified, 0);
  assert.equal(store.version, 0);
  assert.deepEqual(store.snapshot.visibleColumns.length, 7);
});

test('empty commit is a no-op', () => {
  const store = new HeaderStore(fixtureState());
  let notified = 0;
  store.subscribe(() => notified++);
  store.commit([]);
  assert.equal(notified, 0);
  assert.equal(store.version, 0);
});
