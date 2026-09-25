import test from 'node:test';
import assert from 'node:assert/strict';
import { HeaderStore, mapSelection } from '../src/index.ts';
import { fixtureState } from '../fixtures/header.fixture.ts';

test('selection maps exactly across a reorder', () => {
  const store = new HeaderStore(fixtureState());
  const before = store.snapshot;
  // Select the two leaves under "basic" (row 1, cols 0..1).
  const selection = { anchor: { row: 1, col: 0 }, focus: { row: 1, col: 1 } };

  store.commit([{ type: 'moveColumn', columnId: 'beta', toGroupId: 'nested', index: 0 }]);
  const result = mapSelection(before, store.snapshot, selection);

  assert.equal(result.status, 'exact');
  if (result.status !== 'exact') return;
  const ids = result.candidates.map((c) => c.cellId).sort();
  assert.deepEqual(ids, ['c:alpha', 'c:beta']);
  const beta = result.candidates.find((c) => c.cellId === 'c:beta')!;
  assert.equal(beta.col, 3); // beta now lives inside "nested"; "basic" shrank
});

test('selection over a group that later collapses maps to the collapsed cell', () => {
  const store = new HeaderStore(fixtureState());
  const before = store.snapshot;
  // Select the whole "nested" group: rows 1..2, cols 4..5.
  const selection = { anchor: { row: 1, col: 4 }, focus: { row: 2, col: 5 } };

  store.commit([{ type: 'collapse', groupId: 'nested' }]);
  const result = mapSelection(before, store.snapshot, selection);

  assert.equal(result.status, 'exact');
  if (result.status !== 'exact') return;
  assert.equal(result.candidates.length, 1);
  assert.equal(result.candidates[0].cellId, 'g:nested');
  assert.deepEqual(result.candidates[0].columnIds, ['n1', 'n2']);
});

test('partial mapping reports lost columns when some are hidden', () => {
  const store = new HeaderStore(fixtureState());
  const before = store.snapshot;
  const selection = { anchor: { row: 1, col: 0 }, focus: { row: 1, col: 1 } };

  store.commit([{ type: 'hide', columnIds: ['beta'] }]);
  const result = mapSelection(before, store.snapshot, selection);

  assert.equal(result.status, 'partial');
  if (result.status !== 'partial') return;
  assert.deepEqual(result.lostColumnIds, ['beta']);
  assert.deepEqual(result.candidates.map((c) => c.cellId), ['c:alpha']);
});

test('selection invalidates when nothing it covered remains visible', () => {
  const store = new HeaderStore(fixtureState());
  const before = store.snapshot;
  const selection = { anchor: { row: 2, col: 4 }, focus: { row: 2, col: 5 } };

  store.commit([{ type: 'hide', columnIds: ['n1', 'n2'] }]);
  const result = mapSelection(before, store.snapshot, selection);
  assert.equal(result.status, 'invalidated');
});

test('out-of-grid selection invalidates immediately', () => {
  const store = new HeaderStore(fixtureState());
  const result = mapSelection(store.snapshot, store.snapshot, {
    anchor: { row: 9, col: 0 },
    focus: { row: 9, col: 1 },
  });
  assert.equal(result.status, 'invalidated');
});
