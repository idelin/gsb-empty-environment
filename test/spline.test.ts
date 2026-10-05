import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MonotoneRationalSpline,
  createSpline,
  DuplicateAbscissaError,
  InvalidInputError,
  NotMonotoneError,
} from '../src/index.ts';

const TOL = 1e-9;

function sampleSegmentDerivative(spline: MonotoneRationalSpline, i: number, parts: number): number[] {
  const [x0, x1] = spline.certificates[i].interval;
  const out: number[] = [];
  for (let k = 0; k <= parts; k++) {
    out.push(spline.derivative(x0 + ((x1 - x0) * k) / parts));
  }
  return out;
}

test('duplicate abscissa: identical points are deduplicated', () => {
  const spline = createSpline([0, 1, 1, 2], [0, 1, 1, 2]);
  assert.equal(spline.size, 3);
  assert.ok(spline.verify().holds);
});

test('duplicate abscissa with conflicting ordinate is rejected', () => {
  assert.throws(() => createSpline([0, 1, 1, 2], [0, 1, 2, 3]), (err: unknown) => {
    assert.ok(err instanceof DuplicateAbscissaError);
    assert.equal((err as DuplicateAbscissaError).x, 1);
    assert.equal((err as DuplicateAbscissaError).firstValue, 1);
    assert.equal((err as DuplicateAbscissaError).conflictingValue, 2);
    return true;
  });
});

test('invalid inputs are rejected', () => {
  assert.throws(() => createSpline([0, 2, 1], [0, 1, 2]), InvalidInputError);
  assert.throws(() => createSpline([0, 1], [0]), InvalidInputError);
  assert.throws(() => createSpline([0, Number.NaN], [0, 1]), InvalidInputError);
  assert.throws(() => createSpline([], []), InvalidInputError);
});

test('single point is a constant function; two points give a straight line', () => {
  const single = createSpline([1.5], [42]);
  assert.equal(single.evaluate(0), 42);
  assert.equal(single.derivative(0), 0);
  const line = createSpline([0, 2], [1, 5]);
  assert.ok(Math.abs(line.evaluate(0.7) - 2.4) < 1e-12);
  assert.ok(Math.abs(line.derivative(0.7) - 2) < 1e-12);
});

test('fixed endpoint derivative rule (Fritsch-Carlson one-sided)', () => {
  const spline = createSpline([0, 1, 3], [0, 1, 2]);
  // h = [1, 2], secant slopes = [1, 0.5]
  // d0 = ((2*1+2)*1 - 1*0.5)/3 = 7/6 ; d2 = ((2*2+1)*0.5 - 2*1)/3 = 1/6
  // d1 = (5+4)/(5/1 + 4/0.5) = 9/13
  assert.ok(Math.abs(spline.derivatives[0] - 7 / 6) < 1e-15);
  assert.ok(Math.abs(spline.derivatives[1] - 9 / 13) < 1e-15);
  assert.ok(Math.abs(spline.derivatives[2] - 1 / 6) < 1e-15);
});

test('coefficient output: two-node segment is exactly linear', () => {
  const spline = createSpline([0, 2], [1, 5]);
  const seg = spline.getCoefficients()[0];
  assert.equal(seg.h, 2);
  assert.equal(seg.secant, 4);
  assert.equal(seg.alpha, 1);
  assert.equal(seg.beta, 1);
  assert.equal(seg.r, 2);
  assert.deepEqual([...seg.numerator], [1, 4, 0, 0]);
  assert.deepEqual([...seg.denominator], [1, 0, 0]);
  assert.deepEqual([...seg.derivativeNumerator], [4, 0, 0]);
  assert.equal(seg.numerator[3], 0); // cubic coefficient vanishes by construction
});

test('plateau: exact constancy and exact quadratic roots at endpoints', () => {
  const spline = createSpline([0, 1, 2, 3, 4], [1, 1, 1, 2, 2]);
  const certs = spline.getMonotonicityEvidence();

  // Plateau segments are exactly constant with identically zero W.
  for (const i of [0, 1, 3]) {
    assert.equal(certs[i].trend, 'constant');
    assert.equal(certs[i].analysis.classification, 'identically-zero');
    assert.deepEqual([...certs[i].derivativeNumerator], [0, 0, 0]);
    assert.equal(spline.evaluate((i + 0.37) as number), spline.values[i]);
  }

  // Rising segment between plateaus: d0 = d1 = 0, W(theta) = 2 theta (1 - theta).
  const seg = spline.getCoefficients()[2];
  assert.equal(seg.d0, 0);
  assert.equal(seg.d1, 0);
  assert.deepEqual([...seg.derivativeNumerator], [0, 2, -2]);
  assert.equal(spline.evaluate(2.5), 1.5);

  const cert = certs[2];
  assert.equal(cert.analysis.classification, 'concave-endpoint-min');
  // Exact root analysis: roots are exactly theta = 0 and theta = 1, none inside.
  assert.deepEqual(cert.analysis.rootsInUnitInterval, [0, 1]);
  assert.equal(cert.analysis.certifiedMin, 0);
  assert.ok(cert.holds);
  assert.ok(spline.verify().holds);
});

test('steep transition between near-plateaus stays monotone (exact certificate)', () => {
  const spline = createSpline([0, 1, 2, 3], [0, 0, 1e6, 1e6 + 1e-3]);
  const report = spline.verify();
  assert.ok(report.holds, `failures: ${report.failures}`);
  assert.ok(report.certifiedMinMargin >= -1e-6);

  // Every certificate is backed by the quadratic analysis, not sampling.
  for (const cert of spline.getMonotonicityEvidence()) {
    assert.ok(cert.denominatorMin > 0);
    assert.ok(cert.proof.length > 0);
  }

  // Sanity: dense sampling agrees with the exact certificates.
  for (let i = 0; i < 3; i++) {
    for (const d of sampleSegmentDerivative(spline, i, 200)) {
      assert.ok(d >= -1e-6, `segment ${i} derivative ${d}`);
    }
  }
});

test('non-uniform spacing: certificates hold and derivative is C1 at knots', () => {
  const xs = [0, 0.1, 2, 3.5, 10];
  const ys = [-2, -1, 0.5, 0.75, 3];
  const spline = createSpline(xs, ys);
  assert.ok(spline.verify().holds);

  // Derivative at a knot equals the nodal derivative parameter (C1 join).
  for (let i = 0; i < xs.length; i++) {
    assert.ok(
      Math.abs(spline.derivative(xs[i]) - spline.derivatives[i]) < 1e-10,
      `knot ${i}`,
    );
  }
  // Left and right segment derivatives agree exactly at interior knots
  // (checked from the coefficients: W(1)/(h D(1)^2) vs W(0)/(h D(0)^2)).
  const segs = spline.getCoefficients();
  const endDeriv = (seg: (typeof segs)[number], theta: 0 | 1): number => {
    const [w0, w1, w2] = seg.derivativeNumerator;
    const [a0, a1, a2] = seg.denominator;
    const d = a0 + theta * (a1 + theta * a2);
    return (w0 + theta * (w1 + theta * w2)) / (seg.h * d * d);
  };
  for (let i = 1; i < xs.length - 1; i++) {
    const left = endDeriv(segs[i - 1], 1);
    const right = endDeriv(segs[i], 0);
    const scale = Math.max(1, Math.abs(left), Math.abs(right));
    assert.ok(Math.abs(left - right) / scale < 1e-12, `C1 at knot ${i}: ${left} vs ${right}`);
  }
});

test('monotone decreasing data is preserved', () => {
  const spline = createSpline([0, 1, 2, 5], [3, 2, 2, -4]);
  const report = spline.verify();
  assert.ok(report.holds);
  for (let i = 0; i < 3; i++) {
    for (const d of sampleSegmentDerivative(spline, i, 100)) {
      assert.ok(d <= 1e-9, `segment ${i} derivative ${d}`);
    }
  }
});

test('non-monotone input: per-segment trend preserved, no overshoot', () => {
  const spline = createSpline([0, 1, 2, 3], [0, 2, 1, 3]);
  const report = spline.verify();
  assert.ok(report.holds);
  assert.deepEqual(
    spline.getMonotonicityEvidence().map((c) => c.trend),
    ['increasing', 'decreasing', 'increasing'],
  );
  // No overshoot: on each segment values stay between the endpoint ordinates.
  for (let i = 0; i < 3; i++) {
    const [x0, x1] = spline.certificates[i].interval;
    const lo = Math.min(spline.values[i], spline.values[i + 1]);
    const hi = Math.max(spline.values[i], spline.values[i + 1]);
    for (let k = 0; k <= 200; k++) {
      const v = spline.evaluate(x0 + ((x1 - x0) * k) / 200);
      assert.ok(v >= lo - 1e-9 && v <= hi + 1e-9, `segment ${i} overshoot: ${v}`);
    }
  }
});

test('non-monotone input with requireMonotone is rejected', () => {
  assert.throws(() => createSpline([0, 1, 2], [0, 2, 1], { requireMonotone: true }), NotMonotoneError);
  // Monotone data passes the same option.
  assert.ok(createSpline([0, 1, 2], [0, 1, 1], { requireMonotone: true }).verify().holds);
});

test('custom derivatives are genuinely checked by the certificates', () => {
  // Negative endpoint derivative on an increasing segment must be caught.
  const spline = createSpline([0, 1, 2], [0, 1, 2], { derivatives: [-1, 2, 2] });
  const report = spline.verify();
  assert.ok(!report.holds);
  assert.deepEqual(report.failures, [0]);
  const cert = spline.getMonotonicityEvidence()[0];
  assert.ok(cert.analysis.certifiedMin < 0);
});

test('derivative() matches finite differences of evaluate()', () => {
  const spline = createSpline([0, 0.5, 3, 4, 4.25, 9], [1, 1, 2.5, 2.5, 8, 9]);
  for (const x of [0.1, 0.7, 2.2, 3.5, 4.1, 6.3, 8.9]) {
    const eps = 1e-6;
    const fd = (spline.evaluate(x + eps) - spline.evaluate(x - eps)) / (2 * eps);
    const exact = spline.derivative(x);
    const scale = Math.max(1, Math.abs(exact));
    assert.ok(Math.abs(fd - exact) / scale < 1e-6, `x=${x}: ${fd} vs ${exact}`);
  }
});

test('linear data on non-uniform nodes is reproduced exactly', () => {
  const xs = [0, 0.3, 1, 2.7, 10];
  const ys = xs.map((x) => 3 * x - 2);
  const spline = createSpline(xs, ys);
  for (let k = 0; k <= 100; k++) {
    const x = (10 * k) / 100;
    assert.ok(Math.abs(spline.evaluate(x) - (3 * x - 2)) < 1e-10);
    assert.ok(Math.abs(spline.derivative(x) - 3) < 1e-10);
  }
});

test('evidence output is complete and serialisable', () => {
  const spline = createSpline([0, 1, 2, 4], [0, 0, 3, 4]);
  const evidence = spline.getMonotonicityEvidence();
  assert.equal(evidence.length, 3);
  for (const cert of evidence) {
    assert.ok(typeof cert.proof === 'string' && cert.proof.includes('W(theta)'));
    assert.ok(cert.analysis.certifiedMin >= -cert.analysis.tolerance);
  }
  JSON.parse(JSON.stringify(spline.getCoefficients()));
  JSON.parse(JSON.stringify(spline.verify()));
});
