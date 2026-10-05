import { describe, expect, test } from 'bun:test';

import { AssignmentSolver } from '../src/assignment';
import type { InfeasibleResult, OptimalResult } from '../src/assignment';
import { bruteForce } from '../src/brute';
import { verifyInfeasible, verifyOptimal } from '../src/verify';

/** Deterministic PRNG (mulberry32). */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomCost(rand: () => number): bigint {
  const r = rand();
  if (r < 0.4) return BigInt(Math.floor(rand() * 21) - 10); // small, may be negative
  if (r < 0.6) return BigInt(-Math.floor(rand() * 1000)); // negative
  // beyond Number.MAX_SAFE_INTEGER
  return (1n << 60n) + BigInt(Math.floor(rand() * 1e6)) - 500000n;
}

function randomSolver(rand: () => number, n: number): AssignmentSolver {
  const solver = new AssignmentSolver(n);
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (rand() < 0.25) continue; // forbidden edge
      solver.setCost(i, j, randomCost(rand));
    }
  }
  return solver;
}

function checkAgainstBrute(solver: AssignmentSolver): void {
  const result = solver.solve();
  const brute = bruteForce(solver);
  if (result.status === 'optimal') {
    expect(brute.feasible).toBe(true);
    expect(result.cost).toBe(brute.minCost as bigint);
    expect(verifyOptimal(solver, result)).toBe(true);
  } else {
    expect(brute.feasible).toBe(false);
    expect(verifyInfeasible(solver, result)).toBe(true);
  }
}

describe('random small matrices vs brute force', () => {
  test('200 random instances, n in 1..7', () => {
    const rand = rng(12345);
    for (let iter = 0; iter < 200; iter++) {
      const n = 1 + Math.floor(rand() * 7);
      checkAgainstBrute(randomSolver(rand, n));
    }
  });

  test('dense feasible instances, n in 1..8', () => {
    const rand = rng(777);
    for (let iter = 0; iter < 100; iter++) {
      const n = 1 + Math.floor(rand() * 8);
      const solver = new AssignmentSolver(n);
      for (let i = 0; i < n; i++) {
        for (let j = 0; j < n; j++) {
          if (rand() < 0.1) continue;
          solver.setCost(i, j, randomCost(rand));
        }
      }
      checkAgainstBrute(solver);
    }
  });
});

describe('forbidden edges and Hall deficiency', () => {
  test('disabling the unique feasible edge makes the instance infeasible', () => {
    // n=2, edges (0,0), (0,1), (1,1): unique perfect matching 0->0, 1->1.
    const solver = new AssignmentSolver(2, [
      [5, 7],
      [null, 9],
    ]);
    const before = solver.solve() as OptimalResult;
    expect(before.status).toBe('optimal');
    expect(before.assignment).toEqual([0, 1]);
    expect(before.cost).toBe(14n);

    solver.disableEdge(1, 1); // the unique feasible edge of row 1
    const after = solver.solve() as InfeasibleResult;
    expect(after.status).toBe('infeasible');
    expect(after.hall.rows).toEqual([1]);
    expect(after.hall.cols).toEqual([]);
    expect(verifyInfeasible(solver, after)).toBe(true);
  });

  test('chain instance: one critical edge removal breaks feasibility', () => {
    // rows 0..2, edges: (0,0),(0,1),(1,1),(1,2),(2,2). Unique matching.
    const solver = new AssignmentSolver(3, [
      [1, 2, null],
      [null, 3, 4],
      [null, null, 5],
    ]);
    expect((solver.solve() as OptimalResult).cost).toBe(9n);
    solver.disableEdge(0, 0);
    const result = solver.solve() as InfeasibleResult;
    expect(result.status).toBe('infeasible');
    // row 0 can now only use col 1; alternating search reaches rows {0,1}
    // whose only neighbour columns are {1,2}... verify independently.
    expect(verifyInfeasible(solver, result)).toBe(true);
    expect(result.hall.rows.length).toBeGreaterThan(result.hall.cols.length);
  });

  test('two rows sharing a single column give Hall set |S|=2, |N(S)|=1', () => {
    const solver = new AssignmentSolver(3, [
      [1, null, null],
      [2, null, null],
      [3, 4, 5],
    ]);
    const result = solver.solve() as InfeasibleResult;
    expect(result.status).toBe('infeasible');
    expect(result.hall.rows).toEqual([0, 1]);
    expect(result.hall.cols).toEqual([0]);
    expect(verifyInfeasible(solver, result)).toBe(true);
  });

  test('n=1 with its only edge forbidden', () => {
    const solver = new AssignmentSolver(1);
    const result = solver.solve() as InfeasibleResult;
    expect(result.status).toBe('infeasible');
    expect(result.hall.rows).toEqual([0]);
    expect(result.hall.cols).toEqual([]);
    expect(verifyInfeasible(solver, result)).toBe(true);
  });
});

describe('multiple optimal ties', () => {
  test('all-zero 3x3 has 6 optima; solver returns one of them', () => {
    const solver = new AssignmentSolver(3, [
      [0, 0, 0],
      [0, 0, 0],
      [0, 0, 0],
    ]);
    const brute = bruteForce(solver);
    expect(brute.optimumCount).toBe(6);
    const result = solver.solve() as OptimalResult;
    expect(result.cost).toBe(0n);
    expect(verifyOptimal(solver, result)).toBe(true);
  });

  test('tied optima with mixed negative and huge costs', () => {
    const big = (1n << 62n);
    const solver = new AssignmentSolver(3, [
      [big, -5n, big],
      [-5n, big, big],
      [big, big, 7n],
    ]);
    // Two optimal assignments: (0->1,1->0,2->2) and (0->2? no...)
    const brute = bruteForce(solver);
    expect(brute.optimumCount).toBeGreaterThanOrEqual(1);
    const result = solver.solve() as OptimalResult;
    expect(result.cost).toBe(brute.minCost as bigint);
    expect(verifyOptimal(solver, result)).toBe(true);
  });

  test('random tie-heavy matrices match brute force', () => {
    const rand = rng(999);
    for (let iter = 0; iter < 100; iter++) {
      const n = 2 + Math.floor(rand() * 4);
      const solver = new AssignmentSolver(n);
      for (let i = 0; i < n; i++) {
        for (let j = 0; j < n; j++) {
          solver.setCost(i, j, BigInt(Math.floor(rand() * 3) - 1)); // -1, 0, 1
        }
      }
      const brute = bruteForce(solver);
      const result = solver.solve() as OptimalResult;
      expect(result.cost).toBe(brute.minCost as bigint);
      expect(verifyOptimal(solver, result)).toBe(true);
    }
  });
});

describe('local cost updates', () => {
  test('stale potentials are never reused after an update', () => {
    const solver = new AssignmentSolver(1, [[10]]);
    const first = solver.solve() as OptimalResult;
    expect(first.cost).toBe(10n);
    expect(verifyOptimal(solver, first)).toBe(true);

    solver.setCost(0, 0, 3); // cheaper: old dual u=10 would violate u <= 3
    const second = solver.solve() as OptimalResult;
    expect(second).not.toBe(first); // recomputed, not the cached object
    expect(second.cost).toBe(3n);
    expect(verifyOptimal(solver, second)).toBe(true);
    // The stale certificate must NOT verify against the updated problem.
    expect(verifyOptimal(solver, first)).toBe(false);
  });

  test('cost update on a matched edge changes the optimum', () => {
    const solver = new AssignmentSolver(2, [
      [1, 100],
      [100, 1],
    ]);
    expect((solver.solve() as OptimalResult).cost).toBe(2n);
    solver.setCost(0, 0, 500);
    const result = solver.solve() as OptimalResult;
    expect(result.assignment).toEqual([1, 0]);
    expect(result.cost).toBe(200n);
    expect(verifyOptimal(solver, result)).toBe(true);
  });

  test('disable then re-enable restores feasibility', () => {
    const solver = new AssignmentSolver(2, [
      [1, 2],
      [null, 3],
    ]);
    solver.disableEdge(0, 0);
    expect(solver.solve().status).toBe('infeasible');
    solver.enableEdge(0, 0, 1);
    const result = solver.solve() as OptimalResult;
    expect(result.status).toBe('optimal');
    expect(result.cost).toBe(4n);
    expect(verifyOptimal(solver, result)).toBe(true);
  });

  test('stress: random local updates, brute-checked after every step', () => {
    const rand = rng(31337);
    for (let trial = 0; trial < 30; trial++) {
      const n = 1 + Math.floor(rand() * 6);
      const solver = randomSolver(rand, n);
      checkAgainstBrute(solver);
      for (let step = 0; step < 25; step++) {
        const i = Math.floor(rand() * n);
        const j = Math.floor(rand() * n);
        const action = rand();
        if (action < 0.5) solver.setCost(i, j, randomCost(rand));
        else if (action < 0.8) solver.disableEdge(i, j);
        else solver.enableEdge(i, j, randomCost(rand));
        checkAgainstBrute(solver);
      }
    }
  });
});

describe('BigInt costs', () => {
  test('exact result beyond Number.MAX_SAFE_INTEGER', () => {
    const a = (1n << 62n) + 123456789012345n;
    const b = -((1n << 61n) + 98765432109876n);
    const huge = 3n << 62n; // off-diagonal so large the identity must win
    const solver = new AssignmentSolver(2, [
      [a, huge],
      [huge, b],
    ]);
    const result = solver.solve() as OptimalResult;
    expect(result.cost).toBe(a + b);
    expect(verifyOptimal(solver, result)).toBe(true);
    // Number would lose precision here:
    expect(Number.isSafeInteger(Number(a) + Number(b))).toBe(false);
  });

  test('negative optimum', () => {
    const solver = new AssignmentSolver(2, [
      [-5, -3],
      [-4, -10],
    ]);
    const result = solver.solve() as OptimalResult;
    expect(result.cost).toBe(-15n);
    expect(result.assignment).toEqual([0, 1]);
    expect(verifyOptimal(solver, result)).toBe(true);
  });

  test('number input must be a safe integer', () => {
    const solver = new AssignmentSolver(1);
    expect(() => solver.setCost(0, 0, 2 ** 60)).toThrow(RangeError);
    solver.setCost(0, 0, 2n ** 60n);
    expect((solver.solve() as OptimalResult).cost).toBe(2n ** 60n);
  });
});

describe('edge cases', () => {
  test('n=0 is the empty optimum', () => {
    const solver = new AssignmentSolver(0);
    const result = solver.solve() as OptimalResult;
    expect(result.status).toBe('optimal');
    expect(result.assignment).toEqual([]);
    expect(result.cost).toBe(0n);
    expect(verifyOptimal(solver, result)).toBe(true);
  });

  test('solve() caches until the next mutation', () => {
    const solver = new AssignmentSolver(1, [[7]]);
    const first = solver.solve();
    expect(solver.solve()).toBe(first); // same cached object
    solver.setCost(0, 0, 7); // even an identical value invalidates
    expect(solver.solve()).not.toBe(first);
  });
});

describe('certificate verifiers reject tampering', () => {
  const solver = new AssignmentSolver(3, [
    [4, 1, 3],
    [2, 0, 5],
    [3, 2, 2],
  ]);
  const good = solver.solve() as OptimalResult;
  expect(verifyOptimal(solver, good)).toBe(true);

  test('wrong reported cost', () => {
    expect(verifyOptimal(solver, { ...good, cost: good.cost + 1n })).toBe(false);
  });

  test('tampered dual', () => {
    const dual = {
      rowPotential: [...good.dual.rowPotential],
      colPotential: [...good.dual.colPotential],
    };
    dual.rowPotential[0] += 1n;
    expect(verifyOptimal(solver, { ...good, dual })).toBe(false);
  });

  test('assignment using a forbidden edge', () => {
    const s2 = new AssignmentSolver(2, [[1, null], [1, 1]]);
    const fake: OptimalResult = {
      status: 'optimal',
      assignment: [1, 0],
      cost: 2n,
      dual: { rowPotential: [0n, 0n], colPotential: [0n, 0n] },
    };
    expect(verifyOptimal(s2, fake)).toBe(false);
  });

  test('tampered Hall set', () => {
    const s2 = new AssignmentSolver(2, [[1, null], [1, null]]);
    const result = s2.solve() as InfeasibleResult;
    expect(result.hall.rows).toEqual([0, 1]);
    expect(result.hall.cols).toEqual([0]);
    expect(verifyInfeasible(s2, result)).toBe(true);
    const fake: InfeasibleResult = {
      status: 'infeasible',
      hall: { rows: [0, 1], cols: [1] }, // both rows actually reach col 0
    };
    expect(verifyInfeasible(s2, fake)).toBe(false);
  });
});
