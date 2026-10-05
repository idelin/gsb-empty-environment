import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createMonotoneRationalSpline,
  DuplicateAbscissaError,
  NonMonotoneDataError,
} from '../src/index.ts';

test('interpolates every node exactly', () => {
  const xs = [0, 0.1, 0.2, 0.9, 3.7, 10];
  const ys = [0, 3, 3.5, 4, 4.25, 5];
  const s = createMonotoneRationalSpline(xs, ys);
  for (let i = 0; i < xs.length; i++) {
    assert.equal(s.evaluate(xs[i]), ys[i]);
  }
});

test('fixed endpoint rule and Fritsch-Butland interior slopes', () => {
  // x = [0,1,2], y = [0,1,4]: h = [1,1], delta = [1,3].
  const s = createMonotoneRationalSpline([0, 1, 2], [0, 1, 4]);
  // d0 = ((2+1)*1 - 1*3)/2 = 0
  assert.equal(s.derivatives[0], 0);
  // d1 = (3+3)/(3/1 + 3/3) = 3/2
  assert.equal(s.derivatives[1], 1.5);
  // d2 = ((2+1)*3 - 1*1)/2 = 4
  assert.equal(s.derivatives[2], 4);
});

test('endpoint limiting: sign mismatch gives zero slope', () => {
  // delta0 = 2, delta1 = -1 -> raw estimate (3*2 - 1*(-1))/2 = 3.5, but
  // secants change sign so the limiter keeps d0 only if |d0| <= 3*|delta0|;
  // here d0 = 3.5 <= 6 and sign matches delta0, so it is kept...
  const s = createMonotoneRationalSpline([0, 1, 2], [0, 2, 1]);
  assert.equal(s.derivatives[0], 3.5);
  // ...while a raw estimate with the wrong sign is zeroed:
  // delta0 = 1, delta1 = 3 -> d0 = (3*1 - 3)/2 = 0 exactly; use steeper:
  // delta0 = 1, delta1 = 5 -> d0 = (3 - 5)/2 = -1 -> clamped to 0.
  const t = createMonotoneRationalSpline([0, 1, 2], [0, 1, 6]);
  assert.equal(t.derivatives[0], 0);
});

test('duplicate abscissa: equal ordinates merged, conflicting rejected', () => {
  const s = createMonotoneRationalSpline([0, 1, 1, 2], [0, 1, 1, 2]);
  assert.deepEqual(s.xs, [0, 1, 2]);
  assert.deepEqual(s.ys, [0, 1, 2]);
  assert.throws(
    () => createMonotoneRationalSpline([0, 1, 1, 2], [0, 1, 2, 3]),
    (e: unknown) => e instanceof DuplicateAbscissaError,
  );
});

test('rejects unsorted, non-finite, or too-short input', () => {
  assert.throws(() => createMonotoneRationalSpline([0, 2, 1], [0, 1, 2]));
  assert.throws(() => createMonotoneRationalSpline([0, 1], [0, NaN]));
  assert.throws(() => createMonotoneRationalSpline([0], [0]));
  assert.throws(() => createMonotoneRationalSpline([0, 1], [0]));
});

test('two nodes reproduce the straight line', () => {
  const s = createMonotoneRationalSpline([1, 3], [2, 8]);
  assert.equal(s.evaluate(2), 5);
  assert.equal(s.derivative(2), 3);
});

test('monotone increasing data: every segment certified monotone', () => {
  const s = createMonotoneRationalSpline(
    [0, 0.1, 0.2, 0.9, 3.7, 10],
    [0, 3, 3.5, 4, 4.25, 5],
  );
  assert.ok(s.isNonDecreasing);
  for (const seg of s.segments) {
    assert.equal(seg.evidence.sign, 1);
    for (const c of seg.evidence.bernstein) assert.ok(c >= 0, `bernstein ${c}`);
    assert.ok(seg.evidence.denominatorMin > 0);
  }
});

test('monotone decreasing data: derivative <= 0 everywhere', () => {
  const s = createMonotoneRationalSpline([0, 1, 2, 3, 4], [5, 3, 3, 1, 0]);
  assert.ok(s.isNonIncreasing);
  for (const seg of s.segments) {
    assert.ok(seg.evidence.sign <= 0);
  }
  for (let k = 0; k <= 200; k++) {
    const x = (4 * k) / 200;
    assert.ok(s.derivative(x) <= 1e-12, `derivative at ${x}`);
  }
});

test('plateau: flat segment is exactly constant, neighbours stay monotone', () => {
  const s = createMonotoneRationalSpline(
    [0, 1, 2, 3, 4, 5],
    [0, 0, 1, 1, 1, 2],
  );
  const flat = [0, 2, 3];
  for (const i of flat) {
    assert.equal(s.segments[i].evidence.kind, 'constant');
  }
  // Exactly constant across the whole plateau, not just at samples.
  for (const x of [0.25, 0.5, 0.75, 2.1, 2.9, 3.5]) {
    const segIndex = x < 1 ? 0 : x < 3 ? 2 : 3;
    assert.equal(s.evaluate(x), s.segments[segIndex].coeffs.y0);
    assert.equal(s.derivative(x), 0);
  }
  // Zero slope at every node bordering a plateau.
  assert.equal(s.derivatives[1], 0);
  assert.equal(s.derivatives[2], 0);
  assert.equal(s.derivatives[4], 0);
});

test('steep transition: no overshoot beyond segment range', () => {
  const xs = [0, 1, 2, 3, 4];
  const ys = [0, 0, 1e9, 1e9, 1e9 + 1];
  const s = createMonotoneRationalSpline(xs, ys);
  // Sanity sampling only — the guarantee itself is analytic (see exact tests).
  for (let i = 0; i < xs.length - 1; i++) {
    const lo = Math.min(ys[i], ys[i + 1]);
    const hi = Math.max(ys[i], ys[i + 1]);
    for (let k = 0; k <= 500; k++) {
      const x = xs[i] + ((xs[i + 1] - xs[i]) * k) / 500;
      const v = s.evaluate(x);
      assert.ok(v >= lo - 1e-6 && v <= hi + 1e-6, `overshoot at ${x}: ${v}`);
    }
  }
});

test('non-monotone input: segment-wise monotone, zero slope at extrema', () => {
  const s = createMonotoneRationalSpline([0, 1, 2, 3, 4], [1, 3, 2, 5, 4]);
  assert.ok(!s.isNonDecreasing && !s.isNonIncreasing);
  assert.deepEqual(
    s.segments.map((seg) => seg.evidence.sign),
    [1, -1, 1, -1],
  );
  // Local extrema get exactly zero derivative.
  assert.equal(s.derivatives[1], 0);
  assert.equal(s.derivatives[2], 0);
  assert.equal(s.derivatives[3], 0);
  // Still no overshoot on any segment.
  for (let i = 0; i < 4; i++) {
    const y0 = s.ys[i];
    const y1 = s.ys[i + 1];
    const lo = Math.min(y0, y1);
    const hi = Math.max(y0, y1);
    for (let k = 0; k <= 200; k++) {
      const v = s.evaluate(i + k / 200);
      assert.ok(v >= lo - 1e-9 && v <= hi + 1e-9, `overshoot on segment ${i}`);
    }
  }
});

test('requireMonotone rejects non-monotone data', () => {
  assert.throws(
    () => createMonotoneRationalSpline([0, 1, 2], [0, 2, 1], { requireMonotone: true }),
    (e: unknown) => e instanceof NonMonotoneDataError,
  );
  // ...but accepts both monotone directions.
  createMonotoneRationalSpline([0, 1, 2], [0, 2, 2], { requireMonotone: true });
  createMonotoneRationalSpline([0, 1, 2], [3, 2, 0], { requireMonotone: true });
});

test('evaluation outside the range throws', () => {
  const s = createMonotoneRationalSpline([0, 1], [0, 1]);
  assert.throws(() => s.evaluate(-0.5), RangeError);
  assert.throws(() => s.derivative(1.5), RangeError);
});

test('derivative() matches node derivatives at segment starts', () => {
  const s = createMonotoneRationalSpline([0, 1, 2, 4], [0, 1, 1.5, 2]);
  for (let i = 0; i < s.xs.length; i++) {
    assert.ok(
      Math.abs(s.derivative(s.xs[i]) - s.derivatives[i]) < 1e-12,
      `node ${i}`,
    );
  }
});
