import test from 'node:test';
import assert from 'node:assert/strict';
import { deriveSnapshot } from '../src/index.ts';
import { fixtureState } from '../fixtures/header.fixture.ts';
import { assertSnapshotInvariants } from './helpers.ts';

test('initial fixture: spans, depth, keyboard tree, data mapping', () => {
  const snap = deriveSnapshot(fixtureState(), 0);
  assertSnapshotInvariants(snap);

  // 3 header rows: top groups, mid (metrics children + nested), leaves.
  assert.equal(snap.rows, 3);
  assert.equal(snap.columnCount, 7);

  const cell = (id: string) => {
    const found = snap.cells.find((c) => c.id === id);
    assert.ok(found, id);
    return found;
  };

  // Top-level groups span their leaves; root-level leaf spans full depth.
  assert.deepEqual(
    [cell('g:basic').row, cell('g:basic').col, cell('g:basic').rowSpan, cell('g:basic').colSpan],
    [0, 0, 1, 2],
  );
  assert.deepEqual(
    [cell('g:metrics').row, cell('g:metrics').col, cell('g:metrics').rowSpan, cell('g:metrics').colSpan],
    [0, 2, 1, 4],
  );
  assert.deepEqual(
    [cell('g:nested').row, cell('g:nested').col, cell('g:nested').rowSpan, cell('g:nested').colSpan],
    [1, 4, 1, 2],
  );
  // Root-level leaf "solo" stretches across all rows.
  assert.deepEqual(
    [cell('c:solo').row, cell('c:solo').col, cell('c:solo').rowSpan, cell('c:solo').colSpan],
    [0, 6, 3, 1],
  );
  // Leaves under "basic" start at row 1 and stretch to the bottom.
  assert.deepEqual([cell('c:alpha').row, cell('c:alpha').rowSpan], [1, 2]);
  // Leaves under "nested" sit at row 2.
  assert.deepEqual([cell('c:n1').row, cell('c:n1').rowSpan], [2, 1]);

  // Data mapping: leaf order equals data indices 0..6.
  assert.deepEqual(
    snap.visibleColumns.map((vc) => vc.columnId),
    ['alpha', 'beta', 'm1', 'm2', 'n1', 'n2', 'solo'],
  );
  assert.equal(snap.dataIndexByColumnId.get('n2'), 5);

  // Keyboard tree: geometric neighbours.
  assert.equal(snap.keyboard['g:basic'].right, 'g:metrics');
  assert.equal(snap.keyboard['g:metrics'].left, 'g:basic');
  assert.equal(snap.keyboard['g:metrics'].down, 'c:m1');
  assert.equal(snap.keyboard['c:m1'].up, 'g:metrics');
  assert.equal(snap.keyboard['c:m1'].right, 'c:m2');
  assert.equal(snap.keyboard['c:m2'].right, 'g:nested');
  assert.equal(snap.keyboard['g:nested'].down, 'c:n1');
  assert.equal(snap.keyboard['c:alpha'].down, null); // already bottom
  assert.equal(snap.keyboard['c:solo'].left, 'g:metrics');
  assert.equal(snap.keyboard['g:basic'].up, null); // top row
});

test('keyboard path: walk from top-left group to a deep leaf', () => {
  const snap = deriveSnapshot(fixtureState(), 0);
  const path: string[] = ['g:basic'];
  let cursor = 'g:basic';
  for (const move of ['right', 'down', 'right', 'right', 'down', 'right']) {
    const next = snap.keyboard[cursor][move as 'right' | 'down'];
    assert.ok(next, `move ${move} from ${cursor}`);
    path.push(next);
    cursor = next;
  }
  assert.deepEqual(path, ['g:basic', 'g:metrics', 'c:m1', 'c:m2', 'g:nested', 'c:n1', 'c:n2']);
});
