import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeGrid } from '../src/tree-grid.ts';
import { cols, node, rowIds } from './helpers.ts';

function deepTree() {
  // r1 > r1.1 > r1.1.1 ; r1 > r1.2 ; r2
  return new TreeGrid({
    columns: cols('a', 'b', 'c'),
    nodes: [
      node('r1', [node('r1.1', [node('r1.1.1')]), node('r1.2')]),
      node('r2'),
    ],
  });
}

test('collapsing ancestor returns focus to nearest visible ancestor, same column', () => {
  const grid = deepTree();
  grid.focusCell('r1.1.1', 'b');
  grid.collapse('r1.1');
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r1.1', 'r1.2', 'r2']);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1.1', colKey: 'b' });
});

test('nested collapse skips hidden intermediate ancestors', () => {
  const grid = deepTree();
  grid.focusCell('r1.1.1', 'c');
  grid.collapse('r1'); // hides r1.1 and r1.1.1
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'c' });
});

test('collapsing the focused row itself keeps focus in place', () => {
  const grid = deepTree();
  grid.focusCell('r1.1', 'a');
  grid.collapse('r1.1');
  assert.deepEqual(grid.getFocus(), { rowId: 'r1.1', colKey: 'a' });
});

test('missing column on the ancestor row falls back to nearest left column', () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b', 'c'),
    // cell (r1, c) is disabled: the ancestor row has no usable "c" column
    isCellDisabled: (n, c) => n.id === 'r1' && c.key === 'c',
    nodes: [node('r1', [node('r1.1')]), node('r2')],
  });
  grid.focusCell('r1.1', 'c');
  grid.collapse('r1');
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });
});

test('left fallback also skips disabled cells', () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b', 'c'),
    isCellDisabled: (n, c) => n.id === 'r1' && (c.key === 'c' || c.key === 'b'),
    nodes: [node('r1', [node('r1.1')])],
  });
  grid.focusCell('r1.1', 'c');
  grid.collapse('r1');
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'a' });
});

test('column deleted while focus is hidden resolves to nearest left column', () => {
  const grid = deepTree();
  grid.focusCell('r1.1.1', 'c');
  grid.collapse('r1');      // focus hidden -> ancestor r1, column c
  grid.removeColumn('c');   // column gone -> nearest left is b
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });
});

test('focus is cleared when no visible ancestor and no enabled cell remain', () => {
  const grid = new TreeGrid({
    columns: [{ key: 'a', disabled: true }],
    nodes: [node('r1', [node('r1.1')])],
  });
  grid.focusCell('r1.1', 'a');
  grid.collapse('r1');
  assert.equal(grid.getFocus(), null);
});
