import { test } from "node:test";
import assert from "node:assert/strict";
import { intersect } from "../src/intersect.ts";
import { contains } from "../src/validate.ts";

test("range intersected with multipleOf yields a feasible witness", () => {
  const res = intersect({ minimum: "0.15", maximum: "0.25", multipleOf: "0.1" });
  assert.equal(res.feasible, true);
  if (res.feasible) {
    assert.equal(res.witness, "0.2");
    assert.equal(res.canonical, "2e-1");
    assert.ok(contains({ minimum: "0.15", maximum: "0.25", multipleOf: "0.1" }, res.witness));
  }
});

test("zero is the witness whenever it is feasible", () => {
  const res = intersect({ minimum: "-1", maximum: "1", multipleOf: "0.5" });
  assert.deepEqual(res.feasible && res.witness, "0");
});

test("precise no-solution evidence when no multiple lies in the range", () => {
  const schema = { minimum: "0.15", maximum: "0.25", multipleOf: "0.5" };
  const res = intersect(schema);
  assert.equal(res.feasible, false);
  if (!res.feasible) {
    assert.equal(res.reason, "no-multiple-in-range");
    assert.match(res.summary, /multiple of 0\.5/);
    // Evidence: at fraction-digit scale 1 the integer interval [15, 25]
    // (coefficients at scale 2) must be a multiple of 50, and none is.
    const probe = res.probes.find((p) => p.fractionDigits === 1);
    assert.ok(probe, "expected a probe at fraction-digit scale 1");
    assert.deepEqual(probe!.integerInterval, ["15", "25"]);
    assert.equal(probe!.step, "50");
    assert.equal(probe!.candidate, null);
  }
});

test("degenerate point interval returns the point when feasible", () => {
  const res = intersect({ minimum: "5", maximum: "5" });
  assert.deepEqual(res.feasible && res.witness, "5");
  const bad = intersect({ minimum: "5", maximum: "5", multipleOf: "3" });
  assert.equal(bad.feasible, false);
});

test("exclusive bounds and fraction-digit cap compose", () => {
  const res = intersect({ exclusiveMinimum: "0", exclusiveMaximum: "0.01", maxFractionDigits: 3 });
  assert.equal(res.feasible, true);
  if (res.feasible) assert.equal(res.witness, "0.001");
});

test("digit-aware witness: prefers the value within maxTotalDigits", () => {
  const res = intersect({ minimum: "0.5", maximum: "1.5", maxTotalDigits: 1 });
  assert.equal(res.feasible, true);
  if (res.feasible) {
    assert.equal(res.witness, "1"); // not 0.5 (2 digits)
    assert.equal(res.totalDigits, 1);
  }
});

test("total-digits cap can make a schema infeasible with exact evidence", () => {
  const res = intersect({ minimum: "1000", maxTotalDigits: 3 });
  assert.equal(res.feasible, false);
  if (!res.feasible) {
    assert.equal(res.reason, "total-digits-exceeded");
    assert.match(res.summary, /10\^3 - 1|more than 3 total digits/);
  }
  const narrow = intersect({ minimum: "0.55", maximum: "0.58", maxTotalDigits: 1 });
  assert.equal(narrow.feasible, false);
});

test("empty intervals are reported as such", () => {
  const res = intersect({ minimum: "2", maximum: "1" });
  assert.equal(res.feasible, false);
  if (!res.feasible) assert.equal(res.reason, "empty-interval");
  const touch = intersect({ minimum: "1", exclusiveMaximum: "1" });
  assert.equal(touch.feasible, false);
});

test("unconstrained schema is feasible with witness 0", () => {
  const res = intersect({});
  assert.deepEqual(res.feasible && res.witness, "0");
});

test("dense interval between exclusive bounds finds an interior point", () => {
  const res = intersect({ exclusiveMinimum: "0.01", exclusiveMaximum: "0.02" });
  assert.equal(res.feasible, true);
  if (res.feasible) {
    assert.equal(res.witness, "0.011");
    assert.ok(contains({ exclusiveMinimum: "0.01", exclusiveMaximum: "0.02" }, res.witness));
  }
});

test("negative ranges and negative multiples intersect exactly", () => {
  const res = intersect({ minimum: "-1", maximum: "-0.5", multipleOf: "0.25" });
  assert.equal(res.feasible, true);
  if (res.feasible) {
    assert.equal(res.witness, "-1");
    assert.ok(contains({ minimum: "-1", maximum: "-0.5", multipleOf: "0.25" }, res.witness));
  }
});

test("scientific bounds and multiples compose without floats", () => {
  const res = intersect({ minimum: "1e-3", maximum: "5e-3", multipleOf: "2e-3" });
  assert.equal(res.feasible, true);
  if (res.feasible) {
    assert.equal(res.witness, "0.002");
    assert.equal(res.canonical, "2e-3");
  }
});

test("multipleOf with negative scale (1e2) works", () => {
  const res = intersect({ minimum: "150", maximum: "250", multipleOf: "1e2" });
  assert.equal(res.feasible, true);
  if (res.feasible) assert.equal(res.witness, "200");
});

test("every witness actually validates against its schema", () => {
  const schemas = [
    { minimum: "0.1", maximum: "0.9", multipleOf: "0.1" },
    { exclusiveMinimum: "-2", maximum: "3.33", maxFractionDigits: 1 },
    { minimum: "7", maxTotalDigits: 2 },
    { multipleOf: "0.05", maxFractionDigits: 2 },
  ] as const;
  for (const schema of schemas) {
    const res = intersect(schema);
    assert.equal(res.feasible, true, JSON.stringify(schema));
    if (res.feasible) assert.ok(contains(schema, res.witness), res.witness);
  }
});
