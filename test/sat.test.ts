// SAT solver validation: deterministic CNF instances, each checked against
// full enumeration of all 2^n assignments (proof by exhaustion per instance).
// Instance generation uses a fixed-seed LCG, so the suite is reproducible
// and contains no runtime randomness.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SatSolver } from '../src/sat.ts';

function bruteForce(nvars: number, clauses: number[][], assumptions: number[]): boolean {
  for (let a = 0; a < 1 << nvars; a++) {
    const val = (lit: number): boolean => ((a >> (Math.abs(lit) - 1)) & 1) === 1 === lit > 0;
    if (!assumptions.every(val)) continue;
    if (clauses.every((c) => c.some(val))) return true;
  }
  return false;
}

function modelOk(nvars: number, clauses: number[][], assumptions: number[], m: Int8Array): boolean {
  const val = (lit: number): boolean => (m[Math.abs(lit)] === 1) === lit > 0;
  return assumptions.every(val) && clauses.every((c) => c.some(val));
}

let seed = 0x5eed1234;
const rnd = (n: number): number => {
  seed = (seed * 1103515245 + 12345) & 0x7fffffff;
  return seed % n;
};

test('CDCL solver agrees with exhaustive assignment enumeration', () => {
  for (let inst = 0; inst < 300; inst++) {
    const nvars = 1 + rnd(6);
    const nclauses = rnd(14);
    const clauses: number[][] = [];
    for (let i = 0; i < nclauses; i++) {
      const len = 1 + rnd(3);
      const clause: number[] = [];
      for (let j = 0; j < len; j++) {
        clause.push((rnd(2) === 0 ? -1 : 1) * (1 + rnd(nvars)));
      }
      clauses.push(clause);
    }
    const nassumps = rnd(3);
    const assumptions: number[] = [];
    for (let i = 0; i < nassumps; i++) {
      assumptions.push((rnd(2) === 0 ? -1 : 1) * (1 + rnd(nvars)));
    }
    const solver = new SatSolver(nvars, clauses);
    const status = solver.solve(assumptions);
    assert.notEqual(status, 'unknown', `instance ${inst}`);
    const expected = bruteForce(nvars, clauses, assumptions);
    assert.equal(
      status,
      expected ? 'sat' : 'unsat',
      `instance ${inst}: ${JSON.stringify({ nvars, clauses, assumptions })}`,
    );
    if (status === 'sat') {
      assert.ok(
        modelOk(nvars, clauses, assumptions, solver.lastModel!),
        `instance ${inst}: model does not satisfy formula`,
      );
    }
  }
});

test('repeated solve calls with different assumptions stay consistent', () => {
  // (x1 OR x2) AND (NOT x1 OR x2) AND (x1 OR NOT x2)  <=>  x1 AND x2
  const clauses = [[1, 2], [-1, 2], [1, -2]];
  const solver = new SatSolver(2, clauses);
  assert.equal(solver.solve([1]), 'sat');
  assert.equal(solver.solve([-1]), 'unsat');
  assert.equal(solver.solve([-2]), 'unsat');
  assert.equal(solver.solve([2]), 'sat');
  assert.equal(solver.solve([-1, -2]), 'unsat');
});
