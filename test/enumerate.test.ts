import { test } from "node:test";
import assert from "node:assert/strict";
import { rat } from "../src/rational.ts";
import { MeasurementStore } from "../src/measurement.ts";
import { constraint } from "../src/constraint.ts";
import type { LinearConstraint } from "../src/constraint.ts";
import { compileSystem } from "../src/system.ts";
import { analyzeJoint } from "../src/solver.ts";
import { crossCheck, solveSquare, verifyByEnumeration } from "../src/enumerate.ts";

function setup(
  measurements: [string, string, string | null][],
  constraints: LinearConstraint[],
) {
  const store = new MeasurementStore();
  for (const [id, v, e] of measurements) {
    store.set(id, rat(v), e === null ? null : rat(e));
  }
  return compileSystem(store, constraints);
}

test("solveSquare solves an exact 2x2 system", () => {
  // x + y = 3, x - y = 1  ->  x = 2, y = 1
  const sol = solveSquare(
    [
      [rat("1"), rat("1")],
      [rat("1"), rat("-1")],
    ],
    [rat("3"), rat("1")],
  );
  assert.ok(sol);
  assert.equal(sol[0].toString(), "2");
  assert.equal(sol[1].toString(), "1");
});

test("solveSquare returns null for singular systems", () => {
  const sol = solveSquare(
    [
      [rat("1"), rat("1")],
      [rat("2"), rat("2")],
    ],
    [rat("3"), rat("6")],
  );
  assert.equal(sol, null);
});

test("enumeration verifies a boundary-touching closed constraint as universal pass", () => {
  // m in [0, 1], constraint m <= 1: holds at every vertex, vertex m=1 sits
  // exactly ON the boundary (closed -> pass).
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<=", rat("1"));
  const system = setup([["m", "0.5", "0.5"]], [c]);
  const report = verifyByEnumeration(system);
  assert.equal(report.checked, true);
  assert.deepEqual([...report.universalHolds], [true]);
  assert.equal(report.existential, "feasible");
  const j = analyzeJoint(system);
  assert.equal(j.status, "determinate-pass");
  assert.deepEqual(crossCheck(system, j, report), []);
});

test("enumeration verifies a boundary-touching open constraint as NOT universal", () => {
  // m in [0, 1], constraint m < 1: vertex m=1 violates it.
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<", rat("1"));
  const system = setup([["m", "0.5", "0.5"]], [c]);
  const report = verifyByEnumeration(system);
  assert.deepEqual([...report.universalHolds], [false]);
  assert.equal(report.existential, "feasible"); // interior points exist
  const j = analyzeJoint(system);
  assert.equal(j.status, "mixed");
  assert.deepEqual(crossCheck(system, j, report), []);
});

test("enumeration confirms joint infeasibility of the shared-variable example", () => {
  const c1 = constraint("c1", [{ measurement: "m", coeff: rat("1") }], ">=", rat("1"));
  const c2 = constraint("c2", [{ measurement: "m", coeff: rat("1") }], "<=", rat("-1"));
  const system = setup([["m", "0", "2"]], [c1, c2]);
  const report = verifyByEnumeration(system);
  assert.equal(report.existential, "infeasible");
  const j = analyzeJoint(system);
  assert.equal(j.status, "determinate-fail");
  assert.deepEqual(crossCheck(system, j, report), []);
});

test("strict-only interior feasibility is found via the midpoint grid", () => {
  // m in [0, 1], constraints m > 0 and m < 1: no closure vertex is feasible,
  // but the midpoint 1/2 is.
  const c1 = constraint("c1", [{ measurement: "m", coeff: rat("1") }], ">", rat("0"));
  const c2 = constraint("c2", [{ measurement: "m", coeff: rat("1") }], "<", rat("1"));
  const system = setup([["m", "0.5", "0.5"]], [c1, c2]);
  const report = verifyByEnumeration(system);
  assert.equal(report.existential, "feasible");
  assert.ok(report.feasiblePoint);
  const j = analyzeJoint(system);
  assert.equal(j.status, "mixed");
  assert.deepEqual(crossCheck(system, j, report), []);
});

test("enumeration is skipped (not wrong) on unbounded or large domains", () => {
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<=", rat("1"));
  const unbounded = setup([["m", "0", null]], [c]);
  const r1 = verifyByEnumeration(unbounded);
  assert.equal(r1.checked, false);
  assert.match(r1.skippedReason ?? "", /unbounded/);

  const many = setup(
    [
      ["a", "0", "1"],
      ["b", "0", "1"],
      ["c", "0", "1"],
      ["d", "0", "1"],
      ["e", "0", "1"],
    ],
    [constraint("c1", [{ measurement: "a", coeff: rat("1") }], "<=", rat("1"))],
  );
  const r2 = verifyByEnumeration(many);
  assert.equal(r2.checked, false);
  assert.match(r2.skippedReason ?? "", /too many variables/);
});

test("crossCheck flags an inconsistent verdict (sanity of the checker itself)", () => {
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<=", rat("1"));
  const system = setup([["m", "0.5", "0.5"]], [c]);
  const report = verifyByEnumeration(system);
  const problems = crossCheck(system, { status: "determinate-fail" }, report);
  assert.equal(problems.length, 1);
});
