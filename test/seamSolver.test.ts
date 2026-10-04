import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluateCost, solveSeam } from '../src/seamSolver.ts';
import type { SeamProblem, SourceLabel } from '../src/seamSolver.ts';
import { bruteForceMinCost, verifyOptimality } from '../src/bruteForce.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomGrid(rand: () => number, rows: number, cols: number, max: number): number[][] {
  return Array.from({ length: rows }, () =>
    Array.from({ length: cols }, () => Math.floor(rand() * max)),
  );
}

function fixedColumns(height: number, width: number): (SourceLabel | null)[][] {
  return Array.from({ length: height }, (_, y) =>
    Array.from({ length: width }, (_, x) => (x === 0 ? 'A' : x === width - 1 ? 'B' : null)),
  );
}

// ---------- input validation ----------

test('rejects negative source costs', () => {
  const base: SeamProblem = { width: 1, height: 2, costA: [[1], [2]], costB: [[3], [4]] };
  assert.throws(() => solveSeam({ ...base, costA: [[1], [-0.5]] }), RangeError);
  assert.throws(() => solveSeam({ ...base, costB: [[-1], [4]] }), RangeError);
});

test('rejects non-finite source costs', () => {
  const base: SeamProblem = { width: 1, height: 1, costA: [[1]], costB: [[2]] };
  assert.throws(() => solveSeam({ ...base, costA: [[NaN]] }), RangeError);
  assert.throws(() => solveSeam({ ...base, costB: [[Infinity]] }), RangeError);
});

test('rejects negative switch costs', () => {
  const base: SeamProblem = { width: 2, height: 2, costA: [[0, 0], [0, 0]], costB: [[0, 0], [0, 0]] };
  assert.throws(() => solveSeam({ ...base, switchCost: -1 }), RangeError);
  assert.throws(() => solveSeam({ ...base, horizontalSwitch: [[1], [-2]] }), RangeError);
  assert.throws(() => solveSeam({ ...base, verticalSwitch: [[3, -1]] }), RangeError);
});

test('rejects malformed shapes', () => {
  const base: SeamProblem = { width: 2, height: 2, costA: [[0, 0], [0, 0]], costB: [[0, 0], [0, 0]] };
  assert.throws(() => solveSeam({ ...base, costA: [[0, 0]] }), TypeError); // wrong row count
  assert.throws(() => solveSeam({ ...base, costB: [[0, 0], [0]] }), TypeError); // ragged
  assert.throws(() => solveSeam({ ...base, width: 1.5 }), TypeError);
  assert.throws(() => solveSeam({ ...base, height: -1 }), TypeError);
  assert.throws(() => solveSeam({ ...base, horizontalSwitch: [[1, 2], [3, 4]] }), TypeError); // needs width-1 cols
  assert.throws(() => solveSeam({ ...base, fixed: [['A', 'B'], ['A', 'C'] as never] }), TypeError);
});

// ---------- no overlap ----------

test('handles empty (no-overlap) regions', () => {
  for (const [width, height] of [[0, 0], [0, 3], [4, 0]] as const) {
    const r = solveSeam({ width, height, costA: [], costB: [] });
    assert.equal(r.labels.length, height);
    for (const row of r.labels) assert.equal(row.length, width);
    assert.deepEqual(r.costs, {
      sourceCostA: 0, sourceCostB: 0, sourceCost: 0, switchCost: 0, switchCount: 0, total: 0,
    });
  }
});

// ---------- hand-computed cost breakdown ----------

test('reports exact cost breakdown on a hand-computed case', () => {
  // 1x2: costA=[2,8], costB=[7,1], switch=3.
  // AA=10, BB=8, AB=2+1+3=6 (optimal), BA=18.
  const r = solveSeam({ width: 2, height: 1, costA: [[2, 8]], costB: [[7, 1]], switchCost: 3 });
  assert.deepEqual(r.labels, [['A', 'B']]);
  assert.deepEqual(r.costs, {
    sourceCostA: 2, sourceCostB: 1, sourceCost: 3, switchCost: 3, switchCount: 1, total: 6,
  });
});

test('per-edge switch weights enter the breakdown', () => {
  const problem: SeamProblem = {
    width: 2,
    height: 2,
    costA: [[0, 9], [0, 9]],
    costB: [[9, 0], [9, 0]],
    switchCost: 1,
    horizontalSwitch: [[4], [0]],
    verticalSwitch: [[2, 3]],
  };
  const r = solveSeam(problem);
  const check = evaluateCost(problem, r.labels);
  assert.deepEqual(r.costs, check);
  assert.equal(r.costs.switchCount >= 1, true);
  assert.equal(r.costs.total, r.costs.sourceCost + r.costs.switchCost);
});

// ---------- fixed boundary constraints ----------

test('respects fixed sources even when expensive', () => {
  const r = solveSeam({
    width: 2,
    height: 1,
    costA: [[0, 100]],
    costB: [[100, 0]],
    fixed: [['B', 'A']],
  });
  assert.deepEqual(r.labels, [['B', 'A']]);
  assert.equal(r.costs.total, 200);
});

test('fully pinned grid returns the pinned labelling', () => {
  const fixed: (SourceLabel | null)[][] = [
    ['A', 'B', 'A'],
    ['B', 'A', 'B'],
  ];
  const problem: SeamProblem = {
    width: 3,
    height: 2,
    costA: randomGrid(mulberry32(7), 2, 3, 10),
    costB: randomGrid(mulberry32(8), 2, 3, 10),
    switchCost: 2,
    fixed,
  };
  const r = solveSeam(problem);
  assert.deepEqual(r.labels, fixed);
  const brute = bruteForceMinCost(problem);
  assert.equal(brute.feasibleCount, 1);
  assert.equal(r.costs.total, brute.minCost);
});

// ---------- tie handling / determinism ----------

test('ties resolve deterministically towards source A', () => {
  // All costs zero: all-A and all-B are both optimal (brute force sees 2 optima).
  const problem: SeamProblem = {
    width: 3,
    height: 3,
    costA: Array.from({ length: 3 }, () => [0, 0, 0]),
    costB: Array.from({ length: 3 }, () => [0, 0, 0]),
    switchCost: 1,
  };
  const brute = bruteForceMinCost(problem);
  assert.equal(brute.optimalCount, 2);
  const r = solveSeam(problem);
  assert.equal(r.costs.total, 0);
  assert.ok(r.labels.every((row) => row.every((l) => l === 'A')));
});

test('returns the A-maximal optimum among tied optima', () => {
  // 1x2: costA=[0,0], costB=[0,5], no switch cost.
  // Optima: [A,A] and [B,A] (total 0); A-maximal canonical choice is [A,A].
  const problem: SeamProblem = { width: 2, height: 1, costA: [[0, 0]], costB: [[0, 5]] };
  const brute = bruteForceMinCost(problem);
  assert.equal(brute.optimalCount, 2);
  assert.deepEqual(solveSeam(problem).labels, [['A', 'A']]);
});

test('solver is deterministic across repeated runs', () => {
  const rand = mulberry32(42);
  const problem: SeamProblem = {
    width: 6,
    height: 5,
    costA: randomGrid(rand, 5, 6, 4),
    costB: randomGrid(rand, 5, 6, 4),
    switchCost: 2,
  };
  const first = solveSeam(problem);
  const second = solveSeam(problem);
  assert.deepEqual(first.labels, second.labels);
  assert.deepEqual(first.costs, second.costs);
});

// ---------- exhaustive verification on small grids ----------

test('matches brute force on exhaustive small instances', () => {
  const shapes: Array<[number, number]> = [[1, 1], [1, 4], [4, 1], [2, 2], [3, 3], [2, 4], [3, 4]];
  let seed = 1;
  for (const [width, height] of shapes) {
    for (let rep = 0; rep < 4; rep++) {
      const rand = mulberry32(seed++);
      const problem: SeamProblem = {
        width,
        height,
        costA: randomGrid(rand, height, width, 10),
        costB: randomGrid(rand, height, width, 10),
        switchCost: Math.floor(rand() * 6),
      };
      const check = verifyOptimality(problem);
      assert.equal(
        check.result.costs.total,
        check.bruteForce.minCost,
        `mismatch on ${width}x${height} rep ${rep}`,
      );
    }
  }
});

test('matches brute force with per-edge weights and pinned boundary columns', () => {
  let seed = 100;
  for (const [width, height] of [[2, 3], [3, 3], [4, 3], [4, 4]] as Array<[number, number]>) {
    for (let rep = 0; rep < 3; rep++) {
      const rand = mulberry32(seed++);
      const problem: SeamProblem = {
        width,
        height,
        costA: randomGrid(rand, height, width, 8),
        costB: randomGrid(rand, height, width, 8),
        switchCost: 1,
        horizontalSwitch: randomGrid(rand, height, width - 1, 5),
        verticalSwitch: randomGrid(rand, height - 1, width, 5),
        fixed: fixedColumns(height, width),
      };
      const check = verifyOptimality(problem);
      assert.equal(check.optimal, true, `not optimal on ${width}x${height} rep ${rep}`);
      // Pinned columns must appear in the output.
      for (let y = 0; y < height; y++) {
        assert.equal(check.result.labels[y][0], 'A');
        assert.equal(check.result.labels[y][width - 1], 'B');
      }
    }
  }
});

test('matches brute force with fractional costs', () => {
  const rand = mulberry32(999);
  const grid = (rows: number, cols: number): number[][] =>
    Array.from({ length: rows }, () => Array.from({ length: cols }, () => Math.round(rand() * 50) / 10));
  const problem: SeamProblem = {
    width: 3,
    height: 3,
    costA: grid(3, 3),
    costB: grid(3, 3),
    switchCost: 0.7,
  };
  const check = verifyOptimality(problem);
  assert.ok(Math.abs(check.result.costs.total - check.bruteForce.minCost) < 1e-9);
});

// ---------- larger instance sanity ----------

test('larger random instance: constraints hold and breakdown is consistent', () => {
  const rand = mulberry32(2024);
  const height = 20;
  const width = 30;
  const problem: SeamProblem = {
    width,
    height,
    costA: randomGrid(rand, height, width, 100),
    costB: randomGrid(rand, height, width, 100),
    switchCost: 7,
    fixed: fixedColumns(height, width),
  };
  const r = solveSeam(problem);
  assert.deepEqual(evaluateCost(problem, r.labels), r.costs);
  for (let y = 0; y < height; y++) {
    assert.equal(r.labels[y][0], 'A');
    assert.equal(r.labels[y][width - 1], 'B');
  }
  assert.equal(r.costs.total, r.costs.sourceCostA + r.costs.sourceCostB + r.costs.switchCost);
});
