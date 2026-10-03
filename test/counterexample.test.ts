import { test } from "node:test";
import assert from "node:assert/strict";
import { counterexample } from "../src/counterexample.ts";
import { contains } from "../src/validate.ts";

test("minimum: smallest counterexample is 0 when 0 violates", () => {
  const res = counterexample({ minimum: "5" });
  assert.equal(res.found, true);
  if (res.found) {
    assert.equal(res.value, "0");
    assert.deepEqual(res.violates, ["/minimum"]);
  }
});

test("exclusiveMinimum: the bound itself is a counterexample", () => {
  const res = counterexample({ exclusiveMinimum: "-5" });
  assert.equal(res.found, true);
  if (res.found) assert.equal(res.value, "-5");
});

test("maximum side is symmetric", () => {
  const res = counterexample({ maximum: "-3" });
  assert.equal(res.found, true);
  if (res.found) assert.equal(res.value, "0");
});

test("multipleOf: m/2 always violates", () => {
  const res = counterexample({ multipleOf: "0.5" });
  assert.equal(res.found, true);
  if (res.found) {
    assert.equal(res.value, "0.25");
    assert.deepEqual(res.violates, ["/multipleOf"]);
    assert.equal(contains({ multipleOf: "0.5" }, res.value), false);
  }
});

test("digit caps: 10^T and 10^-(F+1) are minimal violators", () => {
  const total = counterexample({ maxTotalDigits: 2 });
  assert.equal(total.found && total.value, "100");
  const frac = counterexample({ maxFractionDigits: 1 });
  assert.equal(frac.found && frac.value, "0.01");
});

test("reported counterexamples genuinely fail validation", () => {
  const schemas = [
    { minimum: "2", maximum: "9", multipleOf: "2" },
    { exclusiveMaximum: "0" },
    { maxTotalDigits: 3, maxFractionDigits: 1 },
  ] as const;
  for (const schema of schemas) {
    const res = counterexample(schema);
    assert.equal(res.found, true, JSON.stringify(schema));
    if (res.found) assert.equal(contains(schema, res.value), false, res.value);
  }
});

test("unconstrained schema has no counterexample", () => {
  const res = counterexample({});
  assert.equal(res.found, false);
});
