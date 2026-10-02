import { test } from "node:test";
import assert from "node:assert/strict";
import {
  checkConstraints,
  normalizeConstraints,
  parseDecimal,
  solve,
} from "../src/index.ts";

function solveSchema(schema: Parameters<typeof normalizeConstraints>[0]) {
  return solve(normalizeConstraints(schema));
}

test("range ∩ multipleOf yields a verified witness", () => {
  const r = solveSchema({ minimum: "0.05", maximum: "0.2", multipleOf: "0.1" });
  assert.equal(r.feasible, true);
  if (r.feasible) assert.equal(r.canonical, "0.1");
});

test("witness satisfies every constraint (self-check)", () => {
  const schema = { minimum: "-1.5", maximum: "-1.2", multipleOf: "0.1", scale: 2 };
  const c = normalizeConstraints(schema);
  const r = solve(c);
  assert.equal(r.feasible, true);
  if (r.feasible) {
    assert.equal(r.canonical, "-1.2");
    assert.deepEqual(checkConstraints(r.witness, c), []);
  }
});

test("empty intersection gives precise no-solution evidence", () => {
  const r = solveSchema({ minimum: "0.15", maximum: "0.25", multipleOf: "1" });
  assert.equal(r.feasible, false);
  if (!r.feasible) {
    assert.match(r.evidence, /j in \[1, 0\]/);
    assert.match(r.evidence, /0\.15, 0\.25/);
  }
});

test("lattice straddling evidence names nearest multiples", () => {
  const r = solveSchema({ minimum: "0.5", maximum: "1.5", multipleOf: "2" });
  assert.equal(r.feasible, false);
  if (!r.feasible) {
    assert.match(r.evidence, /mod 2/);
    assert.match(r.evidence, /nearest multiples/);
  }
});

test("precision limit participates in the intersection", () => {
  const ok = solveSchema({ minimum: "0.5", maximum: "1.5", precision: 1 });
  assert.equal(ok.feasible, true);
  if (ok.feasible) assert.equal(ok.canonical, "0.5"); // 1 total digit

  const no = solveSchema({ minimum: "0.05", maximum: "0.09", precision: 1 });
  assert.equal(no.feasible, false);
  if (!no.feasible) assert.match(no.evidence, /precision <= 1/);
});

test("multipleOf finer than scale still intersects exactly", () => {
  // multiples of 0.15 with at most 1 decimal place: 0.3, 0.6, 0.9
  const r = solveSchema({ minimum: "0.2", maximum: "0.8", multipleOf: "0.15", scale: 1 });
  assert.equal(r.feasible, true);
  if (r.feasible) assert.equal(r.canonical, "0.3");
});

test("scientific bounds and negative ranges without floats", () => {
  const r = solveSchema({ minimum: "-2e-1", maximum: "-1e-1", multipleOf: "0.05" });
  assert.equal(r.feasible, true);
  if (r.feasible) assert.equal(r.canonical, "-0.1");

  const big = solveSchema({ minimum: "1e3", maximum: "1e3" });
  assert.equal(big.feasible, true);
  if (big.feasible) assert.equal(big.canonical, "1000");
});

test("exclusive bounds are honored", () => {
  const r = solveSchema({ exclusiveMinimum: "0.1", maximum: "0.3", multipleOf: "0.1" });
  assert.equal(r.feasible, true);
  if (r.feasible) assert.equal(r.canonical, "0.2");

  const no = solveSchema({ exclusiveMinimum: "0.1", exclusiveMaximum: "0.2", multipleOf: "0.1" });
  assert.equal(no.feasible, false);
});

test("continuous mode picks the simplest value", () => {
  const r = solveSchema({ minimum: "-1", maximum: "1" });
  assert.equal(r.feasible, true);
  if (r.feasible) assert.equal(r.canonical, "0");

  const excl = solveSchema({ exclusiveMinimum: "0.5" });
  assert.equal(excl.feasible, true);
  if (excl.feasible) assert.equal(excl.canonical, "0.51");

  const empty = solveSchema({ minimum: "2", maximum: "1" });
  assert.equal(empty.feasible, false);
  if (!empty.feasible) assert.match(empty.evidence, /empty decimal interval/);
});

test("zero is a valid lattice point", () => {
  const r = solveSchema({ minimum: "-1", maximum: "1", multipleOf: "0.15", scale: 1 });
  assert.equal(r.feasible, true);
  if (r.feasible) assert.equal(r.canonical, "0");
});

test("oversized scale is budget-rejected", () => {
  assert.throws(
    () => solveSchema({ scale: 200_000 }),
    /exceeds budget/,
  );
});
