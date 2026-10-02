import { test } from "node:test";
import assert from "node:assert/strict";
import {
  checkConstraints,
  contains,
  normalizeConstraints,
} from "../src/index.ts";

test("multipleOf lattice containment with minimal counterexample", () => {
  const r = contains({ multipleOf: "0.1" }, { multipleOf: "0.01" });
  assert.equal(r.contained, false);
  assert.equal(r.counterexample?.canonical, "0.01"); // smallest |v| in B not a multiple of 0.1
  assert.equal(r.counterexample?.violates, "multipleOf");
});

test("contained contracts are proved", () => {
  const r = contains(
    { multipleOf: "0.5", minimum: "-10", maximum: "10" },
    { multipleOf: "1", minimum: "-5", maximum: "5" },
  );
  assert.equal(r.contained, true);
});

test("range violation counterexample", () => {
  const r = contains({ minimum: "0", maximum: "10" }, { minimum: "5", maximum: "20" });
  assert.equal(r.contained, false);
  assert.equal(r.counterexample?.violates, "maximum");
  const v = Number(r.counterexample?.canonical);
  assert.ok(v > 10 && v <= 20);
});

test("precision counterexample is minimal", () => {
  const r = contains({ precision: 2 }, { precision: 4, minimum: "0", maximum: "9999" });
  assert.equal(r.contained, false);
  assert.equal(r.counterexample?.canonical, "0.0001");
});

test("scale counterexample", () => {
  const r = contains({ scale: 1 }, { scale: 3, minimum: "0", maximum: "1" });
  assert.equal(r.contained, false);
  assert.equal(r.counterexample?.canonical, "0.001");
});

test("infeasible B is vacuously contained, with evidence", () => {
  const r = contains({ minimum: "0" }, { minimum: "2", maximum: "1" });
  assert.equal(r.contained, true);
  assert.match(r.reason, /vacuously/);
  assert.match(r.reason, /empty decimal interval/);
});

test("counterexamples verify against both contracts", () => {
  const A = { multipleOf: "0.25", minimum: "0", maximum: "10" };
  const B = { multipleOf: "0.05", minimum: "0", maximum: "10" };
  const r = contains(A, B);
  assert.equal(r.contained, false);
  const a = normalizeConstraints(A);
  const b = normalizeConstraints(B);
  assert.deepEqual(checkConstraints(r.counterexample!.value, b), []);
  assert.ok(checkConstraints(r.counterexample!.value, a).length > 0);
  assert.equal(r.counterexample!.canonical, "0.05");
});

test("equal contracts contain each other", () => {
  const s = { minimum: "0", maximum: "1", multipleOf: "0.1", precision: 3 };
  assert.equal(contains(s, s).contained, true);
});
