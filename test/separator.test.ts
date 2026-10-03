import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';
import { formatHyperplane } from '../src/format.ts';
import {
  separate,
  verifySeparation,
  verifyCommonPoint,
} from '../src/separator.ts';

test('acceptance: separable 2D sets yield a strict rational hyperplane', () => {
  const P = [
    [0, 0],
    [1, 0],
    [0, 1],
  ];
  const Q = [
    [3, 3],
    [4, 2],
    [2, 4],
  ];
  const res = separate(P, Q);
  assert.ok(res.separable);
  assert.match(res.hyperplane, /^-?[\d]*[xyz].*= 0$/);
  assert.ok(verifySeparation(P, Q, res.normal, res.offset));
  // every normal coefficient and the offset are exact rationals
  for (const c of res.normal) assert.ok(c instanceof Fraction);
  assert.ok(res.offset instanceof Fraction);
});

test('acceptance: axis-parallel separation', () => {
  const P = [
    [0, 0],
    [0, 1],
  ];
  const Q = [
    [2, 0],
    [2, 1],
  ];
  const res = separate(P, Q);
  assert.ok(res.separable);
  assert.ok(verifySeparation(P, Q, res.normal, res.offset));
  // Hyperplane must be vertical: y-coefficient zero, x-coefficient nonzero.
  assert.ok(res.normal[1].isZero());
  assert.ok(!res.normal[0].isZero());
  assert.equal(res.offset.toString(), '1');
});

test('acceptance: intersecting 2D hulls yield a common barycentric point', () => {
  const P = [
    [0, 0],
    [4, 0],
    [0, 4],
  ];
  const Q = [
    [3, 1],
    [1, 3],
    [3, 3],
  ];
  const res = separate(P, Q);
  assert.ok(!res.separable);
  assert.ok(verifyCommonPoint(P, Q, res.point, res.coeffsP, res.coeffsQ));
  // The common point must lie in the overlap region of the two triangles.
  const [x, y] = res.point;
  assert.ok(x.sign() >= 0 && y.sign() >= 0);
  assert.ok(x.add(y).cmp(Fraction.from(4)) <= 0); // inside conv(P)
});

test('nested hulls: inner set center is a common point', () => {
  const P = [
    [0, 0],
    [6, 0],
    [0, 6],
  ];
  const Q = [
    [1, 1],
    [2, 1],
    [1, 2],
  ];
  const res = separate(P, Q);
  assert.ok(!res.separable);
  assert.ok(verifyCommonPoint(P, Q, res.point, res.coeffsP, res.coeffsQ));
});

test('touching hulls are NOT strictly separable', () => {
  const P = [
    [0, 0],
    [1, 0],
  ];
  const Q = [
    [1, 0],
    [2, 0],
  ];
  const res = separate(P, Q);
  assert.ok(!res.separable);
  assert.equal(res.point[0].toString(), '1');
  assert.equal(res.point[1].toString(), '0');
  assert.ok(verifyCommonPoint(P, Q, res.point, res.coeffsP, res.coeffsQ));
});

test('identical singletons are inseparable, common point is the point itself', () => {
  const P = [[3, -2]];
  const Q = [[3, -2]];
  const res = separate(P, Q);
  assert.ok(!res.separable);
  assert.equal(res.point[0].toString(), '3');
  assert.equal(res.point[1].toString(), '-2');
});

test('rational coordinates: exact arithmetic on fractions', () => {
  const P = [
    ['1/2', '1/3'],
    ['5/2', '1/3'],
    ['1/2', '7/3'],
  ];
  const Q = [
    [1, 1],
    [2, 1],
    [1, 2],
  ];
  const res = separate(P, Q);
  assert.ok(!res.separable);
  assert.ok(verifyCommonPoint(P, Q, res.point, res.coeffsP, res.coeffsQ));
});

test('3D separable sets', () => {
  const P = [
    [0, 0, 0],
    [1, 0, 0],
    [0, 1, 0],
  ];
  const Q = [
    [0, 0, 3],
    [1, 0, 4],
    [0, 1, 3],
  ];
  const res = separate(P, Q);
  assert.ok(res.separable);
  assert.ok(verifySeparation(P, Q, res.normal, res.offset));
  assert.match(res.hyperplane, /z/);
});

test('3D intersecting sets', () => {
  const P = [
    [0, 0, 0],
    [2, 0, 0],
    [0, 2, 0],
    [0, 0, 2],
  ];
  const Q = [
    [1, 0, 0],
    [0, 1, 0],
    [0, 0, 1],
    [1, 1, 1],
  ];
  const res = separate(P, Q);
  assert.ok(!res.separable);
  assert.ok(verifyCommonPoint(P, Q, res.point, res.coeffsP, res.coeffsQ));
});

test('hyperplane formatting matches "2x - 3y + 5 = 0" style', () => {
  const normal = [Fraction.from(2), Fraction.from(-3)];
  assert.equal(formatHyperplane(normal, Fraction.from(5)), '2x - 3y + 5 = 0');
  // denominators cleared and gcd reduced
  const scaled = [Fraction.from('1/2'), Fraction.from('-3/4')];
  assert.equal(formatHyperplane(scaled, Fraction.from('5/4')), '2x - 3y + 5 = 0');
  // sign normalized so first nonzero coefficient is positive
  const neg = [Fraction.from(-2), Fraction.from(3)];
  assert.equal(formatHyperplane(neg, Fraction.from(-5)), '2x - 3y + 5 = 0');
  // unit coefficients and zero terms
  assert.equal(
    formatHyperplane([Fraction.from(1), Fraction.from(0)], Fraction.from(-4)),
    'x - 4 = 0',
  );
  // 3D uses z, higher dims use x4, x5, ...
  assert.equal(
    formatHyperplane(
      [Fraction.from(1), Fraction.from(1), Fraction.from(1), Fraction.from(2)],
      Fraction.from(0),
    ),
    'x + y + z + 2x4 = 0',
  );
});

test('dimension mismatch throws', () => {
  assert.throws(() => separate([[0, 0]], [[1, 2, 3]]));
  assert.throws(() => separate([], []));
});
