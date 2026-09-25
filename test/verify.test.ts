import test from 'node:test';
import assert from 'node:assert/strict';
import {
  and, between, bruteMinCounterexample, checkContainment, checkSetOps, cmp, contains,
  diff, enumeratePoints, evalPoint, ge, gt, inSet, le, lt, ne, not, or, TRUE,
  type Cond, type Domain,
} from '../src/index.ts';

const small: Domain = {
  x: { kind: 'int', lo: 0, hi: 4 },
  y: { kind: 'int', lo: 0, hi: 4 },
  role: { kind: 'enum', values: ['a', 'b'] },
};

// A battery of conditions exercising integer comparisons, difference
// constraints, set predicates and boolean combinations.
const pool: Cond[] = [
  TRUE,
  ge('x', 2),
  le('x', 3),
  ne('x', 1),
  cmp('y', '<', 2),
  gt('y', 1),
  between('x', 1, 3),
  diff('x', '<=', 'y', 1),
  diff('x', '>=', 'y', 0),
  diff('x', '==', 'y', 2),
  inSet('role', ['a']),
  inSet('role', ['a', 'b']),
  and(ge('x', 1), le('y', 2)),
  or(lt('x', 1), inSet('role', ['b'])),
  not(and(ge('x', 2), ge('y', 2))),
  and(diff('x', '<=', 'y', 2), or(inSet('role', ['a']), ge('y', 3))),
];

test('engine agrees with brute force on all condition pairs', () => {
  for (const container of pool) {
    for (const containee of pool) {
      const result = contains(container, containee, small);
      const problems = checkContainment(container, containee, small, result);
      assert.deepEqual(problems, [], `container=${JSON.stringify(container)} containee=${JSON.stringify(containee)}`);
    }
  }
});

test('intersection, union and difference match brute-force set algebra', () => {
  for (const p of pool) {
    for (const q of pool) {
      assert.deepEqual(checkSetOps(p, q, small), []);
    }
  }
});

test('counterexample validity and minimality on optional (three-valued) domain', () => {
  const domain: Domain = {
    x: { kind: 'int', lo: 0, hi: 3, optional: true },
    y: { kind: 'int', lo: 0, hi: 3 },
  };
  const cases: Array<[Cond, Cond]> = [
    [ge('x', 2), ge('x', 1)],
    [and(ge('x', 1), ge('y', 1)), ge('x', 1)],
    [diff('x', '<=', 'y', 0), diff('x', '<=', 'y', 2)],
    [TRUE, ge('x', 0)],
  ];
  for (const [container, containee] of cases) {
    const result = contains(container, containee, domain);
    assert.deepEqual(checkContainment(container, containee, domain, result), []);
  }
  // contains(ge(x,0), TRUE): x=⊥ makes the container unknown -> violation at ⊥.
  const r = contains(ge('x', 0), TRUE, domain);
  assert.equal(r.status, 'not-contained');
  assert.deepEqual((r as { counterexample: unknown }).counterexample, { x: null, y: 0 });
});

test('brute-force minimum counterexample matches enumeration order', () => {
  const min = bruteMinCounterexample(ge('x', 3), ge('x', 1), small);
  assert.deepEqual(min, { x: 1, y: 0, role: 'a' });
});

test('enumeration covers the full finite domain', () => {
  const points = enumeratePoints(small);
  assert.equal(points.length, 5 * 5 * 2);
  // Every point evaluates every condition to a definite three-valued verdict.
  for (const p of points) {
    for (const c of pool) {
      assert.match(evalPoint(c, p), /^[TFU]$/);
    }
  }
});
