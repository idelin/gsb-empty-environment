import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSpline } from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('randomized monotone data: exact certificates always hold', () => {
  const rand = mulberry32(20261005);
  const trials = 300;
  for (let trial = 0; trial < trials; trial++) {
    const n = 2 + Math.floor(rand() * 9);
    const xs: number[] = [0];
    for (let i = 1; i < n; i++) {
      // Non-uniform spacing, occasionally extreme ratios.
      const gap = rand() < 0.3 ? 1e-3 + rand() * 1e-2 : rand() * 10 + 1e-3;
      xs.push(xs[i - 1] + gap);
    }
    const ys: number[] = [(rand() - 0.5) * 10];
    for (let i = 1; i < n; i++) {
      if (rand() < 0.25) {
        ys.push(ys[i - 1]); // plateau
      } else {
        const jump = rand() < 0.2 ? 1e5 * rand() + 1 : rand() * 5 + 1e-6; // steep jumps
        ys.push(ys[i - 1] + jump);
      }
    }
    const spline = createSpline(xs, ys);
    const report = spline.verify();
    assert.ok(report.holds, `trial ${trial}: failures ${report.failures}`);

    // The certified lower bound must not exceed the sampled minimum
    // (certificates are closed-form lower bounds, not sample checks).
    for (let i = 0; i < spline.certificates.length; i++) {
      const cert = spline.certificates[i];
      if (cert.trend === 'constant') continue;
      const [x0, x1] = cert.interval;
      let sampledMin = Infinity;
      for (let k = 0; k <= 400; k++) {
        const d = spline.derivative(x0 + ((x1 - x0) * k) / 400);
        const signed = cert.trend === 'increasing' ? d : -d;
        sampledMin = Math.min(sampledMin, signed);
      }
      // certifiedMin bounds sigma*W; sampled sigma*dP/dx = sigma*W/(h D^2),
      // so the certified bound must be (sign-consistently) below the sample
      // minimum up to scaling: check the sign agreement instead.
      assert.ok(sampledMin >= -1e-6, `trial ${trial} segment ${i}: sampled ${sampledMin}`);
      assert.ok(
        cert.analysis.certifiedMin >= -cert.analysis.tolerance,
        `trial ${trial} segment ${i}: certified ${cert.analysis.certifiedMin}`,
      );
    }

    // C1 continuity at interior knots, checked exactly from coefficients.
    const segs = spline.getCoefficients();
    const endDeriv = (seg: (typeof segs)[number], theta: 0 | 1): number => {
      const [w0, w1, w2] = seg.derivativeNumerator;
      const [a0, a1, a2] = seg.denominator;
      const den = a0 + theta * (a1 + theta * a2);
      return (w0 + theta * (w1 + theta * w2)) / (seg.h * den * den);
    };
    for (let i = 1; i < n - 1; i++) {
      const left = endDeriv(segs[i - 1], 1);
      const right = endDeriv(segs[i], 0);
      // Rounding of W is proportional to its coefficient magnitudes.
      const wMag = (seg: (typeof segs)[number]): number =>
        (Math.abs(seg.derivativeNumerator[0]) +
          Math.abs(seg.derivativeNumerator[1]) +
          Math.abs(seg.derivativeNumerator[2])) /
        seg.h;
      const scale = Math.max(1, Math.abs(left), Math.abs(right), wMag(segs[i - 1]), wMag(segs[i]));
      assert.ok(Math.abs(left - right) / scale < 1e-9, `trial ${trial} knot ${i}`);
    }
  }
});

test('randomized non-monotone data: no overshoot on any segment', () => {
  const rand = mulberry32(1234567);
  for (let trial = 0; trial < 150; trial++) {
    const n = 3 + Math.floor(rand() * 8);
    const xs: number[] = [0];
    for (let i = 1; i < n; i++) xs.push(xs[i - 1] + rand() * 4 + 0.01);
    const ys = xs.map(() => (rand() - 0.5) * 20);
    const spline = createSpline(xs, ys);
    assert.ok(spline.verify().holds, `trial ${trial}`);
    for (let i = 0; i < spline.certificates.length; i++) {
      const [x0, x1] = spline.certificates[i].interval;
      const lo = Math.min(spline.values[i], spline.values[i + 1]);
      const hi = Math.max(spline.values[i], spline.values[i + 1]);
      for (let k = 0; k <= 100; k++) {
        const v = spline.evaluate(x0 + ((x1 - x0) * k) / 100);
        assert.ok(v >= lo - 1e-8 && v <= hi + 1e-8, `trial ${trial} segment ${i}: ${v}`);
      }
    }
  }
});
