import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Alg } from '../src/index.ts';

const r = (n: number) => Alg.rational(n);
const sq = (n: number) => Alg.sqrt(r(n));

test('minimal polynomials are built exactly', () => {
  const a = sq(2).add(sq(3)); // sqrt(2) + sqrt(3)
  const b = Alg.sqrt(r(5).add(r(2).mul(sq(6)))); // sqrt(5 + 2*sqrt(6))
  assert.equal(a.minimalPolynomial().toString(), 'x^4 - 10x^2 + 1');
  assert.equal(b.minimalPolynomial().toString(), 'x^4 - 10x^2 + 1');
  assert.equal(sq(10).minimalPolynomial().toString(), 'x^2 - 10');
  assert.equal(sq(2).mul(sq(2)).minimalPolynomial().toString(), 'x - 2');
});

test('ACCEPTANCE: sqrt(2) + sqrt(3) === sqrt(5 + 2*sqrt(6)) exactly', () => {
  const a = sq(2).add(sq(3));
  const b = Alg.sqrt(r(5).add(r(2).mul(sq(6))));
  // Numerically they differ from any neighbor by ~0; floats cannot prove this.
  assert.equal(a.compare(b), 0);
  assert.equal(a.equals(b), true);
  assert.equal(a.sub(b).sign(), 0);
  assert.equal(a.sub(b).isZero(), true);
});

test('ACCEPTANCE: sqrt(2) + sqrt(3) < sqrt(10)', () => {
  const a = sq(2).add(sq(3));
  assert.equal(a.compare(sq(10)), -1);
  assert.equal(sq(10).compare(a), 1);
});

test('nested radicals: sqrt(7 + 2*sqrt(10)) === sqrt(2) + sqrt(5)', () => {
  const lhs = Alg.sqrt(r(7).add(r(2).mul(sq(10))));
  const rhs = sq(2).add(sq(5));
  assert.equal(lhs.compare(rhs), 0);
});

test('nested radicals: sqrt(9 + 4*sqrt(5)) === 2 + sqrt(5)', () => {
  const lhs = Alg.sqrt(r(9).add(r(4).mul(sq(5))));
  const rhs = r(2).add(sq(5));
  assert.equal(lhs.compare(rhs), 0);
});

test('arithmetic identities', () => {
  assert.equal(sq(2).mul(sq(2)).compare(r(2)), 0);
  assert.equal(Alg.sqrt(r(8)).compare(r(2).mul(sq(2))), 0); // sqrt(8) === 2*sqrt(2)
  assert.equal(r(1).div(sq(2)).mul(sq(2)).compare(r(1)), 0); // (1/sqrt(2))*sqrt(2) === 1
  assert.equal(sq(2).mul(sq(3)).compare(Alg.sqrt(r(6))), 0); // sqrt(2)*sqrt(3) === sqrt(6)
});

test('strict orderings', () => {
  assert.equal(sq(2).compare(sq(3)), -1);
  assert.equal(sq(3).compare(sq(2)), 1);
  assert.equal(sq(2).compare(r(2)), -1);
  assert.equal(r(3).compare(sq(10)), -1); // 3 < sqrt(10)
  assert.equal(r(2).add(sq(3)).compare(r(4)), -1); // 2 + 1.732... < 4
});

test('sign determination', () => {
  assert.equal(sq(2).sub(r(2)).sign(), -1);
  assert.equal(r(2).sub(sq(2)).sign(), 1);
  assert.equal(sq(2).mul(sq(2)).sub(r(2)).sign(), 0);
  assert.equal(r(0).sign(), 0);
});

test('isZero detects exact cancellation', () => {
  assert.equal(sq(2).mul(sq(2)).sub(r(2)).isZero(), true);
  assert.equal(sq(2).sub(r(1)).isZero(), false);
  assert.equal(r(0).isZero(), true);
});

test('sqrt of negative throws', () => {
  assert.throws(() => Alg.sqrt(r(-1)), /negative/);
  assert.throws(() => Alg.sqrt(r(1).sub(sq(2))), /negative/);
});

test('division by exact zero throws', () => {
  assert.throws(() => r(1).div(sq(2).mul(sq(2)).sub(r(2))), /zero/);
});

test('decimal approximation for display is consistent with comparisons', () => {
  const a = sq(2).add(sq(3));
  assert.equal(a.approxDecimal(6), '3.146264');
  assert.equal(sq(10).approxDecimal(6), '3.162277');
});
