import { test } from "node:test";
import assert from "node:assert/strict";
import { rat } from "../src/rational.ts";
import { MeasurementStore } from "../src/measurement.ts";
import { constraint } from "../src/constraint.ts";
import type { LinearConstraint } from "../src/constraint.ts";
import { compileSystem, satisfies } from "../src/system.ts";
import { classifyConstraint } from "../src/classify.ts";
import {
  analyzeJoint,
  verifyJointWitnesses,
  verifyPassCertificate,
} from "../src/solver.ts";
import { verifyFarkas } from "../src/fm.ts";

function setup(
  measurements: [string, string, string | null][],
  constraints: LinearConstraint[],
) {
  const store = new MeasurementStore();
  for (const [id, v, e] of measurements) {
    store.set(id, rat(v), e === null ? null : rat(e));
  }
  const system = compileSystem(store, constraints);
  return { store, system };
}

test("shared error variable: per-item 'possibly pass' must NOT merge into overall pass", () => {
  // m in [-2, 2]. c1: m >= 1, c2: m <= -1.
  // Each constraint is individually satisfiable (needs-review on its own),
  // but JOINTLY they are infeasible.
  const c1 = constraint("c1", [{ measurement: "m", coeff: rat("1") }], ">=", rat("1"));
  const c2 = constraint("c2", [{ measurement: "m", coeff: rat("1") }], "<=", rat("-1"));
  const { store, system } = setup([["m", "0", "2"]], [c1, c2]);

  // Naive per-constraint view: both are "needs-review" (i.e. possibly pass).
  assert.equal(classifyConstraint(c1, store).status, "needs-review");
  assert.equal(classifyConstraint(c2, store).status, "needs-review");

  // Joint view: determinate-fail with a verifiable Farkas certificate.
  const j = analyzeJoint(system);
  assert.equal(j.status, "determinate-fail");
  assert.ok(j.failCertificate);
  assert.ok(verifyFarkas(system.sources, j.failCertificate, system.variables.length));
});

test("determinate-pass carries a verifiable worst-case-vertex certificate", () => {
  const c = constraint(
    "sum",
    [
      { measurement: "a", coeff: rat("1") },
      { measurement: "b", coeff: rat("1") },
    ],
    "<=",
    rat("10"),
  );
  const { system } = setup(
    [
      ["a", "5", "1"],
      ["b", "3", "0.5"],
    ],
    [c],
  );
  const j = analyzeJoint(system);
  assert.equal(j.status, "determinate-pass");
  assert.ok(j.passCertificate);
  assert.ok(verifyPassCertificate(j.passCertificate));
  // Worst case: 5+1 + 3+0.5 = 9.5 <= 10.
  assert.equal(j.passCertificate.entries[0].worstValue?.toString(), "19/2");
});

test("mixed region provides both a satisfying and a violating witness", () => {
  // m in [-2, 2], constraint m <= 1: passes for m<=1, fails at m=2.
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<=", rat("1"));
  const { system } = setup([["m", "0", "2"]], [c]);
  const j = analyzeJoint(system);
  assert.equal(j.status, "mixed");
  assert.ok(j.witnesses);
  assert.ok(verifyJointWitnesses(system, j.witnesses));
  assert.deepEqual([...j.witnesses.violatedConstraintIds], ["c1"]);
  // The satisfying witness really satisfies; the violating one really violates.
  assert.ok(satisfies(system.constraints[0].ineq, j.witnesses.satisfying));
  assert.ok(!satisfies(system.constraints[0].ineq, j.witnesses.violating));
});

test("budget exhaustion is reported as undecided, not as a verdict", () => {
  // Three variables, constraints chosen so FM generates many inequalities.
  const mk = (id: string, coeffs: [string, string][], rhs: string) =>
    constraint(
      id,
      coeffs.map(([m, k]) => ({ measurement: m, coeff: rat(k) })),
      "<=",
      rat(rhs),
    );
  const constraints = [
    mk("c1", [["x", "1"], ["y", "1"], ["z", "1"]], "1"),
    mk("c2", [["x", "1"], ["y", "-1"], ["z", "2"]], "2"),
    mk("c3", [["x", "-2"], ["y", "1"], ["z", "-1"]], "3"),
    mk("c4", [["x", "3"], ["y", "2"], ["z", "-2"]], "1"),
    mk("c5", [["x", "-1"], ["y", "3"], ["z", "1"]], "2"),
  ];
  const { system } = setup(
    [
      ["x", "0", "1"],
      ["y", "0", "1"],
      ["z", "0", "1"],
    ],
    constraints,
  );
  const j = analyzeJoint(system, { budget: { maxInequalities: 1 } });
  assert.equal(j.status, "undecided");
  assert.match(j.undecidedReason ?? "", /budget exceeded/);
});

test("unknown error: universal fails, existential feasible -> mixed with exact witnesses", () => {
  // Error of m is UNKNOWN (unbounded). Constraint m <= 1.
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<=", rat("1"));
  const { system } = setup([["m", "0", null]], [c]);
  const j = analyzeJoint(system);
  assert.equal(j.status, "mixed");
  assert.ok(j.witnesses);
  assert.ok(verifyJointWitnesses(system, j.witnesses));
});

test("strict inequality feasibility is exact (open boundary excluded)", () => {
  // m in [0, 0] (exact point), constraint m < 0: infeasible.
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<", rat("0"));
  const { system } = setup([["m", "0", "0"]], [c]);
  const j = analyzeJoint(system);
  assert.equal(j.status, "determinate-fail");
});

test("open boundary with slack elsewhere is mixed, not failed", () => {
  // m in [-1, 0], constraint m < 0: feasible (e.g. -0.5) but not universal.
  const c = constraint("c1", [{ measurement: "m", coeff: rat("1") }], "<", rat("0"));
  const { system } = setup([["m", "-0.5", "0.5"]], [c]);
  const j = analyzeJoint(system);
  assert.equal(j.status, "mixed");
  assert.ok(j.witnesses);
  assert.ok(satisfies(system.constraints[0].ineq, j.witnesses.satisfying));
});
