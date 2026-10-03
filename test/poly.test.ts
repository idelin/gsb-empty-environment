import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { Poly, isolateRoots, sturmSequence, signVariations } from '../src/poly.ts';
import { polyAdd, polyMul, resultantY } from '../src/resultant.ts';
import { factor, factorSquareFree } from '../src/factor.ts';

const P = (...cs: (number | bigint | string)[]) => Poly.of(cs);

test('arithmetic', () => {
  const a = P(-1, 0, 1); // x^2 - 1
  const b = P(-1, 1); // x - 1
  assert.equal(a.add(b).toString(), 'x^2 + x - 2');
  assert.equal(a.mul(b).toString(), 'x^3 - x^2 - x + 1');
  assert.equal(a.div(b).toString(), 'x + 1');
  assert.equal(a.mod(b).isZero(), true);
});

test('divmod with remainder', () => {
  const f = P(1, 0, 0, 1); // x^3 + 1
  const g = P(1, 1); // x + 1
  const [q, r] = f.divmod(g);
  assert.equal(q.toString(), 'x^2 - x + 1');
  assert.equal(r.isZero(), true);
});

test('gcd and squareFree', () => {
  const f = P(-1, 0, 1); // x^2 - 1
  const g = P(1, -2, 1); // (x-1)^2
  assert.equal(f.gcd(g).toString(), 'x - 1');
  const h = P(-1, 1).mul(P(-1, 1)).mul(P(-1, 1)).mul(P(2, 1)).mul(P(2, 1)); // (x-1)^3 (x+2)^2
  assert.equal(h.squareFree().toString(), 'x^2 + x - 2');
});

test('sturm root counting', () => {
  const f = P(-2, 0, 1); // x^2 - 2
  const seq = sturmSequence(f);
  const rootsIn = (a: string, b: string) =>
    signVariations(seq, Rational.of(a)) - signVariations(seq, Rational.of(b));
  assert.equal(rootsIn('0', '2'), 1);
  assert.equal(rootsIn('-2', '0'), 1);
  assert.equal(rootsIn('0', '1'), 0);
  assert.equal(rootsIn('-10', '10'), 2);
});

test('root isolation: x^2 - 2 has two sign-changing separated intervals', () => {
  const f = P(-2, 0, 1);
  const ivs = isolateRoots(f);
  assert.equal(ivs.length, 2);
  // each interval brackets a sign change of f (contains exactly one root)
  for (const iv of ivs) {
    assert.ok(f.signAt(iv.lo) * f.signAt(iv.hi) < 0);
  }
  // the positive root lies in the second interval, between 1 and 2
  assert.ok(ivs[1].lo.cmp(Rational.of(2)) < 0);
  assert.ok(ivs[1].hi.cmp(Rational.of(1)) > 0);
  assert.ok(ivs[0].hi.cmp(ivs[1].lo) < 0); // strictly separated
});

test('root isolation: rational roots become point intervals', () => {
  const ivs = isolateRoots(P(0, -1, 0, 1)); // x^3 - x = x(x-1)(x+1)
  assert.equal(ivs.length, 3);
  assert.deepEqual(
    ivs.map((iv) => iv.lo.toString()),
    ['-1', '0', '1'],
  );
  for (const iv of ivs) assert.ok(iv.lo.equals(iv.hi));
});

test('root isolation: x^4 - 10x^2 + 1 has 4 separated real roots', () => {
  const ivs = isolateRoots(P(1, 0, -10, 0, 1));
  assert.equal(ivs.length, 4);
  for (let i = 0; i + 1 < ivs.length; i++) {
    assert.ok(ivs[i].hi.cmp(ivs[i + 1].lo) < 0);
  }
});

test('resultant compositions build annihilating polynomials', () => {
  const sqrt2 = P(-2, 0, 1);
  const sqrt3 = P(-3, 0, 1);
  // sqrt(2) + sqrt(3) is a root of x^4 - 10x^2 + 1
  assert.equal(polyAdd(sqrt2, sqrt3).monic().toString(), 'x^4 - 10x^2 + 1');
  // 2 * sqrt(6) is a root of x^2 - 24
  assert.equal(polyMul(P(-2, 1), P(-6, 0, 1)).monic().toString(), 'x^2 - 24');
  // sqrt(2) * sqrt(2): annihilating polynomial (x^2 - 4)^2, square-free x^2 - 4
  assert.equal(polyMul(sqrt2, sqrt2).squareFree().monic().toString(), 'x^2 - 4');
  // 5 + 2*sqrt(6) -> x^2 - 10x + 1, then sqrt -> x^4 - 10x^2 + 1
  const inner = polyAdd(P(-5, 1), polyMul(P(-2, 1), P(-6, 0, 1)));
  assert.equal(inner.monic().toString(), 'x^2 - 10x + 1');
  assert.equal(inner.composeX2().toString(), 'x^4 - 10x^2 + 1');
});

test('resultant of constants and edge cases', () => {
  const two = Poly.constant(2);
  assert.equal(resultantY([two], [two, two]).toString(), '2'); // Res(c, q) = c^deg(q)
});

test('factorSquareFree: reducible and irreducible cases', () => {
  const f1 = P(-4, 0, 1); // x^2 - 4
  assert.deepEqual(
    factorSquareFree(f1).map((p) => p.toString()),
    ['x - 2', 'x + 2'],
  );
  // x^4 - 10x^2 + 1 is irreducible over Q
  assert.deepEqual(factorSquareFree(P(1, 0, -10, 0, 1)).map((p) => p.toString()), [
    'x^4 - 10x^2 + 1',
  ]);
  // (x^2 - 2)(x^2 - 3)
  const f3 = P(-2, 0, 1).mul(P(-3, 0, 1));
  assert.deepEqual(
    factorSquareFree(f3).map((p) => p.toString()).sort(),
    ['x^2 - 2', 'x^2 - 3'],
  );
});

test('factor: square-free factorization with multiplicities (Yun)', () => {
  const f = P(-2, 0, 1).mul(P(-2, 0, 1)).mul(P(-3, 0, 1)); // (x^2-2)^2 (x^2-3)
  const got = factor(f)
    .map((e) => `${e.factor.toString()}^${e.multiplicity}`)
    .sort();
  assert.deepEqual(got, ['x^2 - 2^2', 'x^2 - 3^1']);
});
