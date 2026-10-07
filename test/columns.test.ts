import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeGrid } from '../src/tree-grid.ts';
import { cols, node } from './helpers.ts';

function makeGrid() {
  return new TreeGrid({
    columns: cols('a', 'b', 'c', 'd'),
    nodes: [node('r1'), node('r2')],
  });
}

test('removing a column updates the flat visible grid', () => {
  const grid = makeGrid();
  assert.equal(grid.removeColumn('b'), true);
  assert.deepEqual(
    grid.getVisibleGrid().columns.map((c) => c.key),
    ['a', 'c', 'd'],
  );
  assert.equal(grid.removeColumn('nope'), false);
});

test('focus on a deleted column moves to the nearest left column', () => {
  const grid = makeGrid();
  grid.focusCell('r2', 'c');
  grid.removeColumn('c');
  assert.deepEqual(grid.getFocus(), { rowId: 'r2', colKey: 'b' });
});

test('left fallback skips disabled cells, then falls back right', () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b', 'c', 'd'),
    isCellDisabled: (n, c) => n.id === 'r1' && (c.key === 'b' || c.key === 'c'),
    nodes: [node('r1')],
  });
  grid.focusCell('r1', 'd');
  grid.removeColumn('d');
  // b and c disabled on r1 -> nearest enabled left is a
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'a' });

  const grid2 = new TreeGrid({
    columns: cols('a', 'b'),
    isCellDisabled: (n, c) => c.key === 'a',
    nodes: [node('r1')],
  });
  grid2.focusCell('r1', 'b');
  grid2.removeColumn('b');
  // no enabled column on the left -> last resort: nearest right (none) -> a is
  // disabled too, so focus is cleared
  assert.equal(grid2.getFocus(), null);
});

test('focus cleared when no enabled column remains', () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b', 'c'),
    isCellDisabled: (n, c) => c.key === 'a' || c.key === 'b',
    nodes: [node('r1')],
  });
  grid.focusCell('r1', 'c');
  grid.removeColumn('c');
  assert.equal(grid.getFocus(), null); // a,b disabled, nothing right of c
});

test('deleting the first column falls back to the nearest right column', () => {
  const grid = makeGrid();
  grid.focusCell('r1', 'a');
  grid.removeColumn('a');
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });
});

test('adding a column keeps focus and extends the grid', () => {
  const grid = makeGrid();
  grid.focusCell('r1', 'b');
  grid.addColumn({ key: 'x' }, 1);
  assert.deepEqual(
    grid.getVisibleGrid().columns.map((c) => c.key),
    ['a', 'x', 'b', 'c', 'd'],
  );
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });

  grid.focusCell('r1', 'a');
  assert.equal(grid.moveRight(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'x' });
});
