import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeGrid } from '../src/tree-grid.ts';
import { cols, node, rowIds } from './helpers.ts';

function makeGrid(extra: Parameters<typeof node>[2] = {}) {
  return new TreeGrid({
    columns: cols('a', 'b', 'c'),
    nodes: [
      node('r1', [node('r1.1'), node('r1.2')]),
      node('r2'),
      node('r3', undefined, extra),
    ],
  });
}

test('moves between visible cells in all four directions', () => {
  const grid = makeGrid();
  assert.equal(grid.focusCell('r1', 'b'), true);

  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1.1', colKey: 'b' });

  assert.equal(grid.moveUp(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });

  assert.equal(grid.moveRight(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'c' });

  assert.equal(grid.moveLeft(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });
});

test('stops at grid edges', () => {
  const grid = makeGrid();
  grid.focusCell('r1', 'a');
  assert.equal(grid.moveUp(), false);
  assert.equal(grid.moveLeft(), false);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'a' });

  grid.focusCell('r3', 'c');
  assert.equal(grid.moveDown(), false);
  assert.equal(grid.moveRight(), false);
  assert.deepEqual(grid.getFocus(), { rowId: 'r3', colKey: 'c' });
});

test('skips disabled rows, columns and predicate-disabled cells', () => {
  const grid = new TreeGrid({
    columns: [{ key: 'a' }, { key: 'b', disabled: true }, { key: 'c' }],
    isCellDisabled: (n, c) => n.id === 'r2' && c.key === 'c',
    nodes: [node('r1'), node('r2'), node('r3', undefined, { disabled: true }), node('r4')],
  });

  // vertical: r3 row is disabled -> skipped
  grid.focusCell('r2', 'a');
  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r4', colKey: 'a' });

  // horizontal: column b disabled -> skipped
  grid.focusCell('r1', 'a');
  assert.equal(grid.moveRight(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'c' });

  // predicate: r2/c disabled -> vertical move skips r2 entirely? no:
  // r2/a is enabled, so moving down from r1 lands on r2 in column a.
  grid.focusCell('r1', 'a');
  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r2', colKey: 'a' });
  // but r2/c is not focusable directly
  assert.equal(grid.focusCell('r2', 'c'), false);
});

test('cannot move into or focus hidden rows', () => {
  const grid = new TreeGrid({
    columns: cols('a'),
    nodes: [node('r1', [node('r1.1'), node('r1.2')]), node('r2')],
  });
  grid.collapse('r1');
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r2']);

  // direct focus into a hidden row is refused
  assert.equal(grid.focusCell('r1.1', 'a'), false);

  // vertical movement steps over the collapsed subtree
  grid.focusCell('r1', 'a');
  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r2', colKey: 'a' });
});

test('first move without focus focuses the first enabled cell', () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b'),
    nodes: [node('r1', undefined, { disabled: true }), node('r2')],
  });
  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r2', colKey: 'a' });
});
