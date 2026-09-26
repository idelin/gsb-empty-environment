import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  checkAll,
  solve,
  v,
  k,
  type Constraint,
  type VarDecl,
} from '../src/solver.ts';
import { enumerateAll, verifyAgainstEnumeration } from '../src/enumerator.ts';

const d = (lo: number, hi: number): number[] =>
  Array.from({ length: hi - lo + 1 }, (_, i) => lo + i);

test('eq between var and const', () => {
  const r = solve([{ name: 'x', domain: d(0, 5) }], [{ kind: 'eq', a: v('x'), b: k(3) }]);
  assert.equal(r.status, 'sat');
  assert.equal(r.model!.x, 3);
});

test('diff constraint: a - b >= 2', () => {
  const vars = [{ name: 'a', domain: d(0, 3) }, { name: 'b', domain: d(0, 3) }];
  const r = solve(vars, [{ kind: 'diff', a: 'a', b: 'b', op: '>=', c: 2 }]);
  assert.equal(r.status, 'sat');
  assert.ok(r.model!.a - r.model!.b >= 2);
});

test('unsat diff: a - b >= 5 over [0,3]', () => {
  const vars = [{ name: 'a', domain: d(0, 3) }, { name: 'b', domain: d(0, 3) }];
  const r = solve(vars, [{ kind: 'diff', a: 'a', b: 'b', op: '>=', c: 5 }]);
  assert.equal(r.status, 'unsat');
});

test('sum and diff constraints simultaneously', () => {
  const vars: VarDecl[] = [
    { name: 'x', domain: d(0, 9) },
    { name: 'y', domain: d(0, 9) },
    { name: 'z', domain: d(0, 9) },
  ];
  const cs: Constraint[] = [
    { kind: 'sum', terms: [{ var: 'x', coeff: 1 }, { var: 'y', coeff: 1 }, { var: 'z', coeff: 1 }], op: '==', rhs: 12 },
    { kind: 'diff', a: 'x', b: 'y', op: '>=', c: 3 },
    { kind: 'diff', a: 'y', b: 'z', op: '>=', c: 1 },
  ];
  const r = solve(vars, cs);
  assert.equal(r.status, 'sat');
  assert.equal(r.model!.x + r.model!.y + r.model!.z, 12);
  assert.ok(r.model!.x - r.model!.y >= 3);
  assert.ok(r.model!.y - r.model!.z >= 1);
  assert.ok(checkAll(cs, r.model!));
});

test('conditional branch fires when condition entailed', () => {
  const vars: VarDecl[] = [{ name: 'x', domain: [5] }, { name: 'y', domain: d(0, 9) }];
  const cs: Constraint[] = [
    { kind: 'implies', cond: { var: 'x', op: '>=', value: 3 }, then: { kind: 'eq', a: v('y'), b: k(7) } },
  ];
  const r = solve(vars, cs);
  assert.equal(r.status, 'sat');
  assert.equal(r.model!.y, 7);
});

test('conditional branch is vacuous when condition refuted', () => {
  const vars: VarDecl[] = [{ name: 'x', domain: [1] }, { name: 'y', domain: d(0, 9) }];
  const cs: Constraint[] = [
    { kind: 'implies', cond: { var: 'x', op: '>=', value: 3 }, then: { kind: 'eq', a: v('y'), b: k(7) } },
  ];
  const r = solve(vars, cs);
  assert.equal(r.status, 'sat'); // y unconstrained
});

test('conditional branch can make the problem unsat', () => {
  const vars: VarDecl[] = [{ name: 'x', domain: [5] }, { name: 'y', domain: d(0, 3) }];
  const cs: Constraint[] = [
    { kind: 'implies', cond: { var: 'x', op: '>=', value: 3 }, then: { kind: 'eq', a: v('y'), b: k(7) } },
  ];
  assert.equal(solve(vars, cs).status, 'unsat');
});

test('budget exhaustion is reported as unknown, distinct from unsat', () => {
  const vars: VarDecl[] = [
    { name: 'x', domain: d(0, 50) },
    { name: 'y', domain: d(0, 50) },
  ];
  const cs: Constraint[] = [
    { kind: 'sum', terms: [{ var: 'x', coeff: 1 }, { var: 'y', coeff: 1 }], op: '==', rhs: 51 },
  ];
  const r = solve(vars, cs, { budget: 1 });
  assert.equal(r.status, 'unknown');
  assert.equal(r.reason, 'budget-exhausted');
  assert.notEqual(r.status, 'unsat');
  // same problem, generous budget: solvable
  assert.equal(solve(vars, cs).status, 'sat');
});

test('unsat is never confused with budget exhaustion', () => {
  const vars: VarDecl[] = [{ name: 'x', domain: d(0, 3) }];
  const cs: Constraint[] = [{ kind: 'eq', a: v('x'), b: k(9) }];
  const r = solve(vars, cs, { budget: 1000 });
  assert.equal(r.status, 'unsat');
  assert.equal(r.reason, undefined);
});

test('negative coefficients in sums', () => {
  const vars: VarDecl[] = [{ name: 'x', domain: d(0, 9) }, { name: 'y', domain: d(0, 9) }];
  const cs: Constraint[] = [
    { kind: 'sum', terms: [{ var: 'x', coeff: 1 }, { var: 'y', coeff: -1 }], op: '==', rhs: 4 },
  ];
  const r = solve(vars, cs);
  assert.equal(r.status, 'sat');
  assert.equal(r.model!.x - r.model!.y, 4);
});

test('solver agrees with independent enumeration on random small problems', () => {
  // Deterministic PRNG (mulberry32).
  let seed = 42;
  const rand = (): number => {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];

  for (let trial = 0; trial < 200; trial++) {
    const vars: VarDecl[] = [
      { name: 'x', domain: d(0, 3) },
      { name: 'y', domain: d(0, 3) },
      { name: 'z', domain: d(0, 3) },
    ];
    const cs: Constraint[] = [];
    const n = 1 + Math.floor(rand() * 4);
    for (let i = 0; i < n; i++) {
      const kind = pick(['eq', 'diff', 'sum', 'implies'] as const);
      if (kind === 'eq') {
        cs.push({ kind: 'eq', a: v(pick(['x', 'y', 'z'])), b: k(Math.floor(rand() * 4)) });
      } else if (kind === 'diff') {
        cs.push({
          kind: 'diff',
          a: pick(['x', 'y', 'z']),
          b: pick(['x', 'y', 'z']),
          op: pick(['<=', '>=', '=='] as const),
          c: Math.floor(rand() * 5) - 2,
        });
      } else if (kind === 'sum') {
        cs.push({
          kind: 'sum',
          terms: [
            { var: pick(['x', 'y', 'z']), coeff: pick([1, 1, -1, 2]) },
            { var: pick(['x', 'y', 'z']), coeff: pick([1, 1, -1]) },
          ],
          op: pick(['<=', '>=', '=='] as const),
          rhs: Math.floor(rand() * 7) - 1,
        });
      } else {
        cs.push({
          kind: 'implies',
          cond: { var: pick(['x', 'y', 'z']), op: pick(['>=', '<=', '=='] as const), value: Math.floor(rand() * 4) },
          then: { kind: 'eq', a: v(pick(['x', 'y', 'z'])), b: k(Math.floor(rand() * 4)) },
        });
      }
    }
    const verdict = verifyAgainstEnumeration(solve(vars, cs), vars, cs);
    assert.ok(
      verdict.consistent,
      `trial ${trial}: ${verdict.detail} constraints=${JSON.stringify(cs)}`,
    );
  }
});

test('enumerator itself: sat and unsat', () => {
  const vars: VarDecl[] = [{ name: 'x', domain: d(0, 2) }];
  assert.equal(enumerateAll(vars, [{ kind: 'eq', a: v('x'), b: k(1) }]).status, 'sat');
  assert.equal(enumerateAll(vars, [{ kind: 'eq', a: v('x'), b: k(5) }]).status, 'unsat');
});
