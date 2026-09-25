import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildGrid } from '../src/grid.ts';
import { loadGlyphFixture, loadTableFixture } from '../src/fixtures.ts';
import { move, type Direction } from '../src/navigation.ts';
import type { Coord } from '../src/types.ts';

const grid = buildGrid(loadTableFixture(), loadGlyphFixture());

function walk(start: Coord, ...dirs: Direction[]): Coord {
  return dirs.reduce((pos, dir) => move(grid, pos, dir), start);
}

test('plain moves between adjacent anchors', () => {
  assert.deepEqual(move(grid, { row: 1, col: 0 }, 'right'), { row: 1, col: 1 });
  assert.deepEqual(move(grid, { row: 1, col: 1 }, 'right'), { row: 1, col: 2 });
  assert.deepEqual(move(grid, { row: 0, col: 2 }, 'down'), { row: 1, col: 2 });
});

test('moving out of a merged cell clears its whole span', () => {
  // Alpha spans cols 0-1: right from its anchor lands on Beta (col 2).
  assert.deepEqual(move(grid, { row: 0, col: 0 }, 'right'), { row: 0, col: 2 });
  // Gamma spans rows 0-1: down from its anchor lands on Iota (row 2).
  assert.deepEqual(move(grid, { row: 0, col: 3 }, 'down'), { row: 2, col: 3 });
  // Eta spans rows 2-3 / cols 0-1: up lands on Delta, left stays (edge).
  assert.deepEqual(move(grid, { row: 2, col: 0 }, 'up'), { row: 1, col: 0 });
});

test('landing on a covered slot resolves to the region anchor', () => {
  // (1,3) is covered by Gamma anchored at (0,3).
  assert.deepEqual(move(grid, { row: 1, col: 2 }, 'right'), { row: 0, col: 3 });
  assert.deepEqual(move(grid, { row: 2, col: 3 }, 'up'), { row: 0, col: 3 });
  // (2,1) is covered by Eta anchored at (2,0).
  assert.deepEqual(move(grid, { row: 2, col: 2 }, 'left'), { row: 2, col: 0 });
  // (3,0) is covered by Eta; Kappa's left edge steps onto it.
  assert.deepEqual(move(grid, { row: 3, col: 2 }, 'left'), { row: 2, col: 0 });
  // (3,3) is covered by Kappa anchored at (3,2).
  assert.deepEqual(move(grid, { row: 2, col: 3 }, 'down'), { row: 3, col: 2 });
});

test('grid edges keep the cursor in place', () => {
  assert.deepEqual(move(grid, { row: 0, col: 3 }, 'up'), { row: 0, col: 3 });
  assert.deepEqual(move(grid, { row: 0, col: 3 }, 'right'), { row: 0, col: 3 });
  assert.deepEqual(move(grid, { row: 1, col: 0 }, 'left'), { row: 1, col: 0 });
  assert.deepEqual(move(grid, { row: 2, col: 0 }, 'down'), { row: 2, col: 0 }); // Eta bottom = grid bottom
});

test('every reachable landing is an anchor, never a covered slot', () => {
  const dirs: Direction[] = ['up', 'down', 'left', 'right'];
  for (const cell of grid.cells) {
    for (const dir of dirs) {
      const next = move(grid, cell.anchor, dir);
      assert.ok(
        grid.cellAt(next.row, next.col) !== null,
        `${cell.text} ${dir} -> (${next.row},${next.col}) must be an anchor`,
      );
    }
  }
});

test('a long walk stays consistent', () => {
  const end = walk(
    { row: 1, col: 0 },
    'right', 'right', 'right', // Delta -> Epsilon -> Zeta -> Gamma(0,3)
    'down',                    // -> Iota (2,3)
    'down',                    // -> Kappa (3,2)
    'left',                    // -> Eta (2,0)
    'up',                      // -> Delta (1,0)
  );
  assert.deepEqual(end, { row: 1, col: 0 });
});
