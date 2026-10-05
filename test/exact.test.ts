import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createMonotoneRationalSpline,
  analyzeSplineExactly,
  Fraction,
} from '../src/index.ts';
import { countDistinctRootsInOpenInterval } from '../src/polynomial.ts';

const frac = (x: number) => Fraction.fromNumber(x);

/** Every segment of every dataset must carry a valid exact certificate. */
function assertAllCertified(xs: number[], ys: number[], label: string) {
  const s = createMonotoneRationalSpline(xs, ys);
  const reports = analyzeSplineExactly(s.segments);
  assert.equal(reports.length, s.segments.length);
  for (const r of reports) {
    assert.ok(r.denominatorPositive, `${label} seg ${r.segmentIndex}: denominator`);
    assert.ok(
      r.bernsteinSignConsistent,
      `${label} seg ${r.segmentIndex}: bernstein signs ${r.bernstein}`,
    );
    assert.equal(
      r.interiorRootCount,
      0,
      `${label} seg ${r.segmentIndex}: interior roots of dP/dx`,
    );
    assert.ok(r.certified, `${label} seg ${r.segmentIndex}: certified`);
  }
  return reports;
}

test('exact check: plateau data', () => {
  const reports = assertAllCertified(
    [0, 1, 2, 3, 4, 5],
    [0, 0, 1, 1, 1, 2],
    'plateau',
  );
  // Flat segments are exactly constant (zero derivative numerator).
  for (const i of [0, 2, 3]) {
    assert.equal(reports[i].kind, 'constant');
  }
  // Slopes vanish exactly where the plateau meets the ramps.
  assert.ok(reports[1].derivativeZeroAtStart);
  assert.ok(reports[4].derivativeZeroAtStart);
});

test('exact check: steep transition (1e9 ratio between neighbours)', () => {
  assertAllCertified([0, 1, 2, 3, 4], [0, 0, 1e9, 1e9, 1e9 + 1], 'steep');
});

test('exact check: non-uniform spacing (1:100 step ratio)', () => {
  assertAllCertified(
    [0, 0.1, 0.2, 0.9, 3.7, 10],
    [0, 3, 3.5, 4, 4.25, 5],
    'non-uniform',
  );
});

test('exact check: decreasing and non-monotone data', () => {
  assertAllCertified([0, 1, 2, 3, 4], [5, 3, 3, 1, 0], 'decreasing');
  assertAllCertified([0, 1, 2, 3, 4], [1, 3, 2, 5, 4], 'non-monotone');
});

test('certificate Bernstein coefficients are exactly (y1-y0)*[alpha, 1, beta]', () => {
  const s = createMonotoneRationalSpline(
    [0, 0.1, 0.2, 0.9, 3.7, 10],
    [0, 3, 3.5, 4, 4.25, 5],
  );
  const reports = analyzeSplineExactly(s.segments);
  for (const r of reports) {
    if (r.kind !== 'rational') continue;
    assert.ok(r.derivativeNumerator.length <= 3, 'M is at most quadratic');
    const seg = s.segments[r.segmentIndex];
    const dy = frac(seg.coeffs.y1 - seg.coeffs.y0);
    const expected = [
      dy.mul(frac(seg.coeffs.alpha)),
      dy,
      dy.mul(frac(seg.coeffs.beta)),
    ];
    for (let k = 0; k < 3; k++) {
      assert.ok(
        r.bernstein[k].equals(expected[k]),
        `seg ${r.segmentIndex} bernstein[${k}]: ${r.bernstein[k]} != ${expected[k]}`,
      );
    }
  }
});

test('quadratic discriminant equals (y1-y0)^2 * 4 * (1 - alpha*beta) exactly', () => {
  const s = createMonotoneRationalSpline([0, 1, 2, 4], [0, 1, 1.5, 2]);
  const reports = analyzeSplineExactly(s.segments);
  for (const r of reports) {
    if (r.kind !== 'rational') continue;
    assert.ok(r.quadratic, 'quadratic analysis present');
    const seg = s.segments[r.segmentIndex];
    const dy = frac(seg.coeffs.y1 - seg.coeffs.y0);
    const expected = dy
      .mul(dy)
      .mul(frac(4))
      .mul(Fraction.one.sub(frac(seg.coeffs.alpha).mul(frac(seg.coeffs.beta))));
    assert.ok(
      r.quadratic!.discriminant.equals(expected),
      `seg ${r.segmentIndex}: ${r.quadratic!.discriminant} != ${expected}`,
    );
    assert.equal(r.interiorRootCount, 0);
  }
});

test('emitted power-basis coefficients reproduce the certificate function', () => {
  // The evaluator and the emitted coefficients define the same rational
  // function as the exact certificate, up to floating-point rounding.
  const s = createMonotoneRationalSpline(
    [0, 0.1, 0.2, 0.9, 3.7, 10],
    [0, 3, 3.5, 4, 4.25, 5],
  );
  for (const seg of s.segments) {
    const c = seg.coeffs;
    for (const t of [0, 0.13, 0.5, 0.87, 1]) {
      const num =
        c.numerator[0] +
        c.numerator[1] * t +
        c.numerator[2] * t * t +
        c.numerator[3] * t * t * t;
      const den = c.denominator[0] + c.denominator[1] * t + c.denominator[2] * t * t;
      const fromCoeffs = num / den;
      const fromEval = s.evaluate(c.x0 + c.h * t);
      const scale = Math.max(1, Math.abs(fromEval));
      assert.ok(
        Math.abs(fromCoeffs - fromEval) <= 1e-12 * scale,
        `seg at t=${t}: ${fromCoeffs} vs ${fromEval}`,
      );
    }
  }
});

test('Sturm root counting on known quadratics', () => {
  const z = Fraction.zero;
  const o = Fraction.one;
  const quarter = Fraction.of(1, 4);

  // (t - 1/2)^2: one (tangent) root inside (0,1).
  const tangent = countDistinctRootsInOpenInterval(
    [quarter, Fraction.of(-1), o],
    z,
    o,
  );
  assert.equal(tangent.count, 1);

  // t^2 + 1: no real roots.
  const none = countDistinctRootsInOpenInterval([o, z, o], z, o);
  assert.equal(none.count, 0);

  // (t - 1/4)(t - 3/4) = t^2 - t + 3/16: two roots inside (0,1).
  const two = countDistinctRootsInOpenInterval(
    [Fraction.of(3, 16), Fraction.of(-1), o],
    z,
    o,
  );
  assert.equal(two.count, 2);

  // t(t-1): roots only at the endpoints, none inside.
  const endpoints = countDistinctRootsInOpenInterval([z, Fraction.of(-1), o], z, o);
  assert.equal(endpoints.count, 0);
  assert.ok(endpoints.rootAtA && endpoints.rootAtB);

  // t^2 - 1/4: roots at +/-1/2, exactly one inside (0,1).
  const one = countDistinctRootsInOpenInterval([quarter.neg(), z, o], z, o);
  assert.equal(one.count, 1);
});

test('verifyMonotonicity certifies every segment of a mixed dataset', () => {
  const s = createMonotoneRationalSpline(
    [0, 0.5, 0.51, 0.52, 1, 7],
    [0, 0, 1e6, 1e6, 1e6 + 1, 2e6],
  );
  const reports = s.verifyMonotonicity();
  assert.ok(reports.every((r) => r.certified));
});
