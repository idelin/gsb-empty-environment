import { test } from "node:test";
import assert from "node:assert/strict";
import { Fraction } from "../src/fraction.ts";
import { analyze, derivativePolynomial } from "../src/bezier.ts";
import { evalAt } from "../src/polynomial.ts";

const F = (s: string | number) => Fraction.from(s);
const FRACTION_RE = /^-?\d+(\/\d+)?$/;

test("quadratic rational Bezier (w=1,2,1): exact max at t=1/2, y=2/3", () => {
  const a = analyze([F(0), F(1), F(0)], [F(1), F(2), F(1)]);
  // P(t) = 1 - 2t
  assert.deepEqual(a.derivativePolynomial, [1n, -2n]);
  assert.equal(a.criticalPoints.length, 1);
  const c = a.criticalPoints[0];
  assert.equal(c.kind, "max");
  assert.ok(c.t!.eq(F("1/2")));
  assert.ok(c.value!.eq(F("2/3")));
  // monotonicity: up then down
  assert.deepEqual(a.monotonicity.map((m) => m.direction), ["increasing", "decreasing"]);
  // global extrema, endpoints covered
  assert.ok(a.globalMax!.value!.eq(F("2/3")));
  assert.equal(a.globalMax!.t, "1/2");
  assert.ok(a.globalMin!.value!.eq(F(0)));
  assert.equal(a.globalMin!.t, "0");
});

test("uniform weights reduce to polynomial Bezier", () => {
  // y = (2t-1)^2 via control points (1, -1, 1)
  const a = analyze([F(1), F(-1), F(1)], [F(1), F(1), F(1)]);
  assert.deepEqual(a.denominator, [1n]);
  const c = a.criticalPoints[0];
  assert.equal(c.kind, "min");
  assert.ok(c.t!.eq(F("1/2")));
  assert.ok(c.value!.eq(F(0)));
  assert.ok(a.globalMax!.value!.eq(F(1)));
});

test("asymmetric weights: irrational critical point is interval-isolated", () => {
  const a = analyze([F(0), F(1), F(0)], [F(1), F(2), F(3)]);
  assert.equal(a.criticalPoints.length, 1);
  const c = a.criticalPoints[0];
  assert.equal(c.kind, "max");
  assert.equal(c.t, null); // not rational
  // exact root is (sqrt(3)-1)/2 ~ 0.3660254...
  const exact = (Math.sqrt(3) - 1) / 2;
  assert.ok(c.interval!.lo.toNumber() < exact && exact < c.interval!.hi.toNumber());
  // certified interval bounds are exact rationals
  assert.match(c.interval!.lo.toString(), FRACTION_RE);
  assert.match(c.interval!.hi.toString(), FRACTION_RE);
  // value ~ 4 - 2*sqrt(3) ~ 0.535898
  assert.ok(Math.abs(c.valueApprox - (4 - 2 * Math.sqrt(3))) < 1e-9);
  // global min at endpoint
  assert.ok(a.globalMin!.value!.eq(F(0)));
});

test("fractional control points and weights stay exact", () => {
  const a = analyze([F("1/2"), F("-3/4"), F(2)], [F(2), F("1/2"), F(3)]);
  // endpoint interpolation: y(0)=1/2, y(1)=2 exactly
  assert.ok(a.globalMax!.value!.eq(F(2)));
  assert.equal(a.globalMax!.t, "1");
  const c = a.criticalPoints[0];
  assert.equal(c.kind, "min");
  // exact root (46 - sqrt(1626))/49 ~ 0.11584305
  const exact = (46 - Math.sqrt(1626)) / 49;
  assert.ok(c.interval!.lo.toNumber() < exact && exact < c.interval!.hi.toNumber());
});

test("double stationary point does not change monotonicity", () => {
  // y = (2t-1)^3, control points (-1, 1, -1, 1)
  const a = analyze([F(-1), F(1), F(-1), F(1)], [F(1), F(1), F(1), F(1)]);
  assert.equal(a.criticalPoints.length, 1);
  const c = a.criticalPoints[0];
  assert.equal(c.kind, "stationary");
  assert.equal(c.multiplicity, 2);
  assert.equal(c.signChange, false);
  assert.ok(c.t!.eq(F("1/2")));
  assert.deepEqual(a.monotonicity.map((m) => m.direction), ["increasing", "increasing"]);
  assert.ok(a.globalMin!.value!.eq(F(-1)));
  assert.ok(a.globalMax!.value!.eq(F(1)));
});

test("derivative roots on the boundary are reported, endpoints covered", () => {
  // y = 3t^2 - 2t^3, control points (0, 0, 1, 1); P = 6t(1-t)
  const a = analyze([F(0), F(0), F(1), F(1)], [F(1), F(1), F(1), F(1)]);
  const ts = a.criticalPoints.map((c) => c.t!.toString()).sort();
  assert.deepEqual(ts, ["0", "1"]);
  assert.ok(a.criticalPoints.every((c) => c.kind === "stationary"));
  assert.deepEqual(a.monotonicity.map((m) => m.direction), ["increasing"]);
  assert.equal(a.globalMin!.t, "0");
  assert.equal(a.globalMax!.t, "1");
  assert.ok(a.globalMin!.value!.eq(F(0)));
  assert.ok(a.globalMax!.value!.eq(F(1)));
});

test("interior min and max both found (cubic)", () => {
  // y = 3t - 3t^2 + ... build from derivative: y' = (3t-1)(3t-2) up to scale.
  // y = integral of (3t-1)(3t-2) = 3t^3 - (9/2)t^2 + 2t; use Bezier control points:
  // y0=0, y1=y0+2/3, y2=y1+(-1/2)/3*... simpler: verify via direct curve.
  // Take control points (0, 2, -2, 0), weights (1,1,1,1):
  // y = 6t(1-t)^2*... = 2*B1 - 2*B2 = 6t(1-t)^2 - 6t^2(1-t) = 6t(1-t)(1-2t)
  const a = analyze([F(0), F(2), F(-2), F(0)], [F(1), F(1), F(1), F(1)]);
  const kinds = a.criticalPoints.map((c) => c.kind).sort();
  assert.deepEqual(kinds, ["max", "min"]);
  // roots of y' = 0 at t = (3 +- sqrt(3))/6
  const lo = (3 - Math.sqrt(3)) / 6;
  const hi = (3 + Math.sqrt(3)) / 6;
  const sorted = [...a.criticalPoints].sort((p, q) => p.tApprox - q.tApprox);
  assert.ok(Math.abs(sorted[0].tApprox - lo) < 1e-9);
  assert.ok(Math.abs(sorted[1].tApprox - hi) < 1e-9);
  assert.equal(sorted[0].kind, "max");
  assert.equal(sorted[1].kind, "min");
});

test("constant curve", () => {
  const a = analyze([F(2), F(2), F(2)], [F(1), F(3), F(1)]);
  assert.equal(a.constant, true);
  assert.ok(a.globalMax!.value!.eq(F(2)));
});

test("derivativePolynomial matches hand computation", () => {
  // points (0,1,0), weights (1,2,1): P = 1 - 2t (primitive)
  const P = derivativePolynomial([F(0), F(1), F(0)], [F(1), F(2), F(1)]);
  assert.deepEqual(P, [1n, -2n]);
  // sanity: P(1/2) == 0 exactly
  assert.ok(evalAt(P, F("1/2")).isZero);
});
