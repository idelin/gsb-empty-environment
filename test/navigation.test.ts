import { test } from "node:test";
import assert from "node:assert/strict";
import { Grid } from "../src/grid.ts";
import { fixtureGrid } from "../src/fixtures/nodeTree.ts";
import type { Direction, Pos } from "../src/index.ts";

// Fixture merges: B1:C1, B2:C3, A4:C4 on a 4x4 grid.
const grid = new Grid(fixtureGrid);

function walk(from: Pos, ...keys: Direction[]): Pos {
  return keys.reduce((pos, key) => grid.move(pos, key), from);
}

test("moving into a merged region lands on its anchor, never a covered cell", () => {
  assert.deepEqual(grid.move({ row: 0, col: 0 }, "right"), { row: 0, col: 1 }); // A1 -> B1 anchor
  assert.deepEqual(grid.move({ row: 0, col: 3 }, "left"), { row: 0, col: 1 }); // D1 -> B1 anchor (skips covered C1)
  assert.deepEqual(grid.move({ row: 1, col: 3 }, "left"), { row: 1, col: 1 }); // D2 -> B2 anchor
});

test("leaving a merged region skips its whole span in one step", () => {
  assert.deepEqual(grid.move({ row: 0, col: 1 }, "right"), { row: 0, col: 3 }); // B1 -> D1 (over C1)
  assert.deepEqual(grid.move({ row: 1, col: 1 }, "down"), { row: 3, col: 0 }); // B2 -> A4 anchor (over B3:C3, lands in A4:C4)
  assert.deepEqual(grid.move({ row: 1, col: 1 }, "right"), { row: 1, col: 3 }); // B2 -> D2
});

test("every reachable cell is a plain cell or an anchor", () => {
  for (let row = 0; row < grid.rows; row++) {
    for (let col = 0; col < grid.cols; col++) {
      const from = { row, col };
      for (const key of ["up", "down", "left", "right"] as Direction[]) {
        const landed = grid.move(from, key);
        assert.equal(
          grid.merges.isCovered(landed),
          false,
          `move ${key} from ${row},${col} landed on covered cell`,
        );
      }
    }
  }
});

test("moves that would leave the grid keep the current cell", () => {
  assert.deepEqual(grid.move({ row: 0, col: 0 }, "up"), { row: 0, col: 0 });
  assert.deepEqual(grid.move({ row: 0, col: 0 }, "left"), { row: 0, col: 0 });
  assert.deepEqual(grid.move({ row: 3, col: 0 }, "down"), { row: 3, col: 0 }); // A4 anchor stays
  assert.deepEqual(grid.move({ row: 0, col: 3 }, "right"), { row: 0, col: 3 });
});

test("a key walk visits only anchors and plain cells", () => {
  const path: Pos[] = [];
  let pos: Pos = { row: 0, col: 0 };
  for (const key of ["right", "right", "down", "down", "left", "left", "up"] as Direction[]) {
    pos = grid.move(pos, key);
    path.push(pos);
  }
  assert.deepEqual(path, [
    { row: 0, col: 1 }, // B1 (anchor of B1:C1)
    { row: 0, col: 3 }, // D1
    { row: 1, col: 3 }, // D2
    { row: 2, col: 3 }, // D3
    { row: 1, col: 1 }, // left into covered C3 resolves to B2 anchor
    { row: 1, col: 0 }, // A2
    { row: 0, col: 0 }, // A1
  ]);
});
