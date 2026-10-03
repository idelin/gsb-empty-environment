import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';
import { solveLP } from '../src/lp.ts';

const F = (s: string) => Fraction.from(s);

test('bounded maximization with <= constraints', () => {
  // max x + y  s.t.  x + 2y <= 4, 4x + 2y <= 12, x,y >= 0
  const res = solveLP(
    2,
    [F('1'), F('1')],
    [
      { coeffs: [F('1'), F('2')], sense: '<=', rhs: F('4') },
      { coeffs: [F('4'), F('2')], sense: '<=', rhs: F('12') },
      { coeffs: [F('-1'), F('0')], sense: '<=', rhs: F('0') },
      { coeffs: [F('0'), F('-1')], sense: '<=', rhs: F('0') },
    ],
    true,
  );
  assert.equal(res.status, 'optimal');
  assert.equal(res.objective?.toString(), '10/3');
  assert.equal(res.x[0].toString(), '8/3');
  assert.equal(res.x[1].toString(), '2/3');
});

test('minimization with >= constraints', () => {
  // min x + y  s.t.  x + 2y >= 4, x,y >= 0  -> optimum 2 at (0, 2)
  const res = solveLP(
    2,
    [F('1'), F('1')],
    [
      { coeffs: [F('1'), F('2')], sense: '>=', rhs: F('4') },
      { coeffs: [F('1'), F('0')], sense: '>=', rhs: F('0') },
      { coeffs: [F('0'), F('1')], sense: '>=', rhs: F('0') },
    ],
    false,
  );
  assert.equal(res.status, 'optimal');
  assert.equal(res.objective?.toString(), '2');
});

test('equality constraints and exact rational solution', () => {
  // x + y = 2, x - y = 1  ->  x = 3/2, y = 1/2
  const res = solveLP(
    2,
    [F('0'), F('0')],
    [
      { coeffs: [F('1'), F('1')], sense: '=', rhs: F('2') },
      { coeffs: [F('1'), F('-1')], sense: '=', rhs: F('1') },
    ],
    false,
  );
  assert.equal(res.status, 'optimal');
  assert.equal(res.x[0].toString(), '3/2');
  assert.equal(res.x[1].toString(), '1/2');
});

test('detects infeasibility', () => {
  const res = solveLP(
    1,
    [F('0')],
    [
      { coeffs: [F('1')], sense: '<=', rhs: F('1') },
      { coeffs: [F('1')], sense: '>=', rhs: F('3') },
    ],
    false,
  );
  assert.equal(res.status, 'infeasible');
});

test('detects unboundedness', () => {
  const res = solveLP(
    1,
    [F('1')],
    [{ coeffs: [F('1')], sense: '>=', rhs: F('0') }],
    true,
  );
  assert.equal(res.status, 'unbounded');
});

test('handles negative rhs and redundant equalities', () => {
  // -x <= -1  (i.e. x >= 1), 2x = 4, x + 0*y = 2 duplicated
  const res = solveLP(
    1,
    [F('0')],
    [
      { coeffs: [F('-1')], sense: '<=', rhs: F('-1') },
      { coeffs: [F('2')], sense: '=', rhs: F('4') },
      { coeffs: [F('1')], sense: '=', rhs: F('2') },
    ],
    false,
  );
  assert.equal(res.status, 'optimal');
  assert.equal(res.x[0].toString(), '2');
});
