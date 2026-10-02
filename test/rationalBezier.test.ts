import { test } from "node:test";
import assert from "node:assert/strict";
import { Rat } from "../src/rational.ts";
import { analyze, createCurve, derivativePolynomial } from "../src/rationalBezier.ts";

test("symmetric curve [0,1,0] w=[1,2,1]: exact max 2/3 at t=1/2", () => {
  const r = analyze([0, 1, 0], [1, 2, 1]);
  // P(t) = 4 - 8t, stored primitively as 1 - 2t
  assert.deepEqual(r.derivativePoly, [1n, -2n]);
  assert.equal(r.criticalPoints.length, 1);
  const cp = r.criticalPoints[0];
  assert.equal(cp.kind, "max");
  assert.equal(cp.t.exact?.toString(), "1/2");
  assert.equal(cp.value.toString(), "2/3");
  assert.equal(cp.valueLo.toString(), "2/3");
  assert.equal(cp.valueHi.toString(), "2/3");
  // monotone segments
  assert.deepEqual(r.segments.map((s) => s.direction), ["increasing", "decreasing"]);
  // global extrema: max 2/3 at t=1/2, min 0 at both endpoints (tie)
  assert.equal(r.globalMax.value.toString(), "2/3");
  assert.equal(r.globalMax.locations.length, 1);
  assert.equal(r.globalMax.locations[0].exact?.toString(), "1/2");
  assert.equal(r.globalMin.value.toString(), "0");
  assert.equal(r.globalMin.locations.length, 2);
  assert.equal(r.globalMin.locations[0].exact?.toString(), "0");
  assert.equal(r.globalMin.locations[1].exact?.toString(), "1");
});

test("asymmetric weights [1,2,3]: irrational critical point certified", () => {
  const r = analyze([0, 1, 0], [1, 2, 3]);
  // P(t) = 4 - 8t - 8t^2 (primitive: 1 - 2t - 2t^2), root (sqrt(3)-1)/2
  assert.deepEqual(r.derivativePoly, [1n, -2n, -2n]);
  assert.equal(r.criticalPoints.length, 1);
  const cp = r.criticalPoints[0];
  assert.equal(cp.kind, "max");
  assert.equal(cp.t.exact, null);
  const tStar = (Math.sqrt(3) - 1) / 2;
  assert.ok(cp.t.lo.toNumber() <= tStar && cp.t.hi.toNumber() >= tStar);
  assert.ok(cp.t.hi.sub(cp.t.lo).toNumber() < 2 ** -48);
  // exact extreme value is 4 - 2*sqrt(3) = 0.5358983848...
  const vStar = 4 - 2 * Math.sqrt(3);
  assert.ok(cp.valueLo.toNumber() <= vStar && cp.valueHi.toNumber() >= vStar);
  assert.ok(Math.abs(cp.value.toNumber() - vStar) < 1e-12);
  assert.deepEqual(r.segments.map((s) => s.direction), ["increasing", "decreasing"]);
  assert.equal(r.globalMax.locations.length, 1);
  assert.ok(Math.abs(r.globalMax.value.toNumber() - vStar) < 1e-9);
  assert.equal(r.globalMin.value.toString(), "0");
  assert.equal(r.globalMin.locations.length, 2); // both endpoints
});

test("linear curve [0,1,2]: no interior roots, endpoints are global extrema", () => {
  const r = analyze([0, 1, 2], [1, 1, 1]);
  assert.equal(r.criticalPoints.length, 0);
  assert.equal(r.segments.length, 1);
  assert.equal(r.segments[0].direction, "increasing");
  assert.equal(r.globalMin.value.toString(), "0");
  assert.equal(r.globalMin.locations[0].exact?.toString(), "0");
  assert.equal(r.globalMax.value.toString(), "2");
  assert.equal(r.globalMax.locations[0].exact?.toString(), "1");
});

test("decreasing curve [2,1,0]", () => {
  const r = analyze([2, 1, 0], [1, 1, 1]);
  assert.equal(r.segments.length, 1);
  assert.equal(r.segments[0].direction, "decreasing");
  assert.equal(r.globalMax.value.toString(), "2");
  assert.equal(r.globalMax.locations[0].exact?.toString(), "0");
  assert.equal(r.globalMin.value.toString(), "0");
  assert.equal(r.globalMin.locations[0].exact?.toString(), "1");
});

test("endpoint derivative root: [0,0,1] is y=t^2, monotone increasing", () => {
  const r = analyze([0, 0, 1], [1, 1, 1]);
  assert.deepEqual(r.endpointRoots.map((t) => t.toString()), ["0"]);
  assert.equal(r.criticalPoints.length, 0);
  assert.equal(r.segments.length, 1);
  assert.equal(r.segments[0].direction, "increasing");
  assert.equal(r.globalMin.value.toString(), "0");
  assert.equal(r.globalMax.value.toString(), "1");
});

test("cubic [0,2,-1,1]: two interior extrema, global max still at endpoint", () => {
  const r = analyze([0, 2, -1, 1], [1, 1, 1, 1]);
  // y = 6t - 15t^2 + 10t^3, y' = 30t^2 - 30t + 6 (primitive: 1 - 5t + 5t^2)
  assert.deepEqual(r.derivativePoly, [1n, -5n, 5n]);
  assert.equal(r.criticalPoints.length, 2);
  assert.equal(r.criticalPoints[0].kind, "max");
  assert.equal(r.criticalPoints[1].kind, "min");
  // critical values are 1 - t*, i.e. (5 +/- sqrt(5))/10
  const v1 = (5 + Math.sqrt(5)) / 10;
  const v2 = (5 - Math.sqrt(5)) / 10;
  assert.ok(Math.abs(r.criticalPoints[0].value.toNumber() - v1) < 1e-12);
  assert.ok(Math.abs(r.criticalPoints[1].value.toNumber() - v2) < 1e-12);
  assert.deepEqual(
    r.segments.map((s) => s.direction),
    ["increasing", "decreasing", "increasing"],
  );
  // global max is y(1) = 1 > 0.7236..., global min is y(0) = 0
  assert.equal(r.globalMax.value.toString(), "1");
  assert.equal(r.globalMax.locations[0].exact?.toString(), "1");
  assert.equal(r.globalMin.value.toString(), "0");
  assert.equal(r.globalMin.locations[0].exact?.toString(), "0");
});

test("constant curve [5,5,5]: single constant segment, tie everywhere", () => {
  const r = analyze([5, 5, 5], [1, 1, 1]);
  assert.equal(r.criticalPoints.length, 0);
  assert.equal(r.segments[0].direction, "constant");
  assert.equal(r.globalMax.value.toString(), "5");
  assert.equal(r.globalMin.value.toString(), "5");
});

test("accepts rational inputs and validates weights", () => {
  const r = analyze(["0", "3/2", "-1/4"], ["1", "2", "1"]);
  assert.equal(r.valueAt(0).toString(), "0");
  assert.equal(r.valueAt(1).toString(), "-1/4");
  assert.throws(() => createCurve([0, 1], [1, 0]), /positive/);
  assert.throws(() => createCurve([0, 1], [1]), /as many weights/);
  assert.throws(() => createCurve([0], [1]), /at least 2/);
});

test("derivativePolynomial exposes integer coefficients and scale", () => {
  const { poly, scale } = derivativePolynomial(createCurve([0, 1, 0], [1, 2, 3]));
  assert.deepEqual(poly, [1n, -2n, -2n]);
  assert.ok(scale.sign() > 0);
  // sign of y' at t=0 matches sign of P(0)
  assert.ok(evalSign(poly, 0) > 0);
});

function evalSign(poly: bigint[], t: number): number {
  const v = poly.reduce((acc, c, i) => acc + Number(c) * t ** i, 0);
  return Math.sign(v);
}

test("Rat is exported and usable for exact value queries", () => {
  const r = analyze([0, 1, 0], [1, 2, 1]);
  assert.ok(r.valueAt(Rat.of(1n, 2n)).equals(Rat.of(2n, 3n)));
});
