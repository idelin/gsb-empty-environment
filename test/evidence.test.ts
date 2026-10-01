import { test } from "node:test";
import assert from "node:assert/strict";
import { rat } from "../src/rational.ts";
import { MeasurementStore } from "../src/measurement.ts";
import { ConstraintSet, constraint } from "../src/constraint.ts";
import { compileSystem } from "../src/system.ts";
import { analyzeJoint } from "../src/solver.ts";
import { EvidenceStore } from "../src/evidence.ts";
import type { JointJudgment } from "../src/solver.ts";

function fixture() {
  const measurements = new MeasurementStore();
  measurements.set("temp", rat("9"), rat("0.5"));
  const constraints = new ConstraintSet();
  constraints.add(
    constraint("limit", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10")),
  );
  const evidence = new EvidenceStore<JointJudgment>(measurements, constraints);
  const judge = () =>
    evidence.getOrCompute("joint", () => analyzeJoint(compileSystem(measurements, constraints.all())));
  return { measurements, constraints, evidence, judge };
}

test("judgment is cached while inputs are unchanged", () => {
  const { judge } = fixture();
  const first = judge();
  const second = judge();
  assert.equal(first, second); // same entry object, not recomputed
  assert.equal(first.value.status, "determinate-pass");
});

test("modifying a measurement incrementally revokes old evidence, history is kept", () => {
  const { measurements, evidence, judge } = fixture();
  const first = judge();
  assert.equal(first.value.status, "determinate-pass");

  // Change the measurement: 9 +- 0.5  ->  10.5 +- 0.5 (now crosses the limit).
  measurements.set("temp", rat("10.5"), rat("0.5"));

  // Old evidence is revoked but still retrievable.
  assert.equal(evidence.active("joint"), null);
  const history = evidence.historyOf("joint");
  assert.equal(history.length, 1);
  assert.equal(history[0], first);
  assert.ok(history[0].revokedAtEpoch !== null);
  assert.match(history[0].revokeReason ?? "", /temp/);
  assert.equal(history[0].value.status, "determinate-pass"); // past judgment preserved

  // Re-judging computes fresh evidence.
  const second = judge();
  assert.notEqual(second, first);
  assert.equal(second.value.status, "mixed");

  // Audit log: issued, revoked, issued.
  const events = evidence.history().map((e) => e.event);
  assert.deepEqual(events, ["issued", "revoked", "issued"]);
});

test("adding any measurement conservatively revokes (whole-store snapshot)", () => {
  const { measurements, evidence, judge } = fixture();
  const first = judge();
  measurements.set("humidity", rat("40"), rat("1")); // not used by the constraint
  // The snapshot changed (new id), so revocation DOES happen for safety:
  // dependency snapshots cover the whole store. Adding a brand-new
  // measurement is a store change and conservatively revokes.
  assert.equal(evidence.active("joint"), null);
  assert.notEqual(judge(), first);
});

test("constraint changes revoke evidence via syncConstraints", () => {
  const { constraints, evidence, judge } = fixture();
  const first = judge();
  constraints.add(
    constraint("limit2", [{ measurement: "temp", coeff: rat("1") }], ">=", rat("8")),
  );
  assert.equal(evidence.active("joint"), null);
  const second = judge();
  assert.notEqual(second, first);
  assert.equal(second.value.status, "determinate-pass"); // 8.5..9.5 within [8,10]
});

test("revocation cascades only to stale entries", () => {
  const { measurements, evidence, judge } = fixture();
  judge();
  measurements.set("temp", rat("9.2"), rat("0.5"));
  judge(); // fresh entry after first change
  measurements.set("temp", rat("9.4"), rat("0.5"));
  const history = evidence.historyOf("joint");
  assert.equal(history.length, 2);
  assert.ok(history.every((h) => h.revokedAtEpoch !== null));
  assert.equal(evidence.history().filter((e) => e.event === "revoked").length, 2);
});
