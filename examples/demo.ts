/**
 * End-to-end demo: exact threshold judgement for measurements with errors.
 * Run: npm run example   (Node >= 22.6, no dependencies)
 */
import {
  MeasurementStore,
  ConstraintSet,
  EvidenceStore,
  analyzeJoint,
  classifyConstraint,
  compileSystem,
  constraint,
  crossCheck,
  rat,
  verifyByEnumeration,
  verifyFarkas,
  verifyJointWitnesses,
  verifyPassCertificate,
} from "../src/index.ts";
import type { JointJudgment } from "../src/index.ts";

const line = (s: string) => console.log(s);
const hr = () => line("-".repeat(72));

function showJudgment(name: string, j: JointJudgment): void {
  line(`  [${name}] status = ${j.status}`);
  if (j.passCertificate) {
    for (const e of j.passCertificate.entries) {
      line(
        `    certificate: worst(${e.constraintId}) = ${e.worstValue} ` +
          `${e.strict ? "<" : "<="} ${e.rhs}  (vertex ${e.vertex})`,
      );
    }
  }
  if (j.failCertificate) {
    line(
      `    Farkas certificate: derived 0 ${j.failCertificate.derivedStrict ? "<" : "<="} ` +
        `${j.failCertificate.derivedRhs} from`,
    );
    for (const m of j.failCertificate.multipliers) {
      line(`      ${m.multiplier} x ${m.source}`);
    }
  }
  if (j.witnesses) {
    line(`    satisfying witness (deviations): [${j.witnesses.satisfying.join(", ")}]`);
    line(`    violating  witness (deviations): [${j.witnesses.violating.join(", ")}]`);
    line(`    violated constraints: ${j.witnesses.violatedConstraintIds.join(", ")}`);
  }
  if (j.undecidedReason) line(`    reason: ${j.undecidedReason}`);
}

// ---------------------------------------------------------------- scenario
line("== Measurements with error bounds, exact rational arithmetic ==");
const measurements = new MeasurementStore();
measurements.set("pressure", rat("9.5"), rat("0.4")); // [9.1, 9.9]
measurements.set("temperature", rat("80"), rat("3")); // [77, 83]
measurements.set("flow", rat("1.1"), null); // error UNKNOWN (unbounded)

const constraints = new ConstraintSet();
constraints.add(
  constraint("p-max", [{ measurement: "pressure", coeff: rat("1") }], "<=", rat("10")),
);
constraints.add(
  constraint(
    "t-open",
    [{ measurement: "temperature", coeff: rat("1") }],
    "<",
    rat("83"),
    "strictly below 83 (open bound)",
  ),
);
constraints.add(
  constraint(
    "mix",
    [
      { measurement: "pressure", coeff: rat("2") },
      { measurement: "temperature", coeff: rat("0.1") },
    ],
    ">=",
    rat("25"),
  ),
);

hr();
line("1) Per-constraint classification (interval semantics, no centre-value pass)");
for (const c of constraints.all()) {
  const r = classifyConstraint(c, measurements);
  line(`  ${c.id}: ${r.status}  -- ${r.reason}`);
}
{
  const r = classifyConstraint(
    constraint("f-max", [{ measurement: "flow", coeff: rat("1") }], "<=", rat("2")),
    measurements,
  );
  line(`  f-max: ${r.status}  -- ${r.reason} (unknown error is NOT 0)`);
}

hr();
line("2) Joint analysis over SHARED deviation variables");
const system = compileSystem(measurements, constraints.all());
const joint = analyzeJoint(system);
showJudgment("joint", joint);
if (joint.passCertificate) {
  line(`    pass certificate verifies: ${verifyPassCertificate(joint.passCertificate)}`);
}
if (joint.witnesses) {
  line(`    witnesses verify: ${verifyJointWitnesses(system, joint.witnesses)}`);
}

hr();
line("3) Shared error variable: per-item 'possibly pass' does NOT merge");
{
  const store = new MeasurementStore();
  store.set("m", rat("0"), rat("2"));
  const c1 = constraint("lo", [{ measurement: "m", coeff: rat("1") }], ">=", rat("1"));
  const c2 = constraint("hi", [{ measurement: "m", coeff: rat("1") }], "<=", rat("-1"));
  line(`  per-item: lo=${classifyConstraint(c1, store).status}, hi=${classifyConstraint(c2, store).status}`);
  const sys = compileSystem(store, [c1, c2]);
  const j = analyzeJoint(sys);
  showJudgment("joint(m>=1, m<=-1)", j);
  if (j.failCertificate) {
    line(`    Farkas certificate verifies: ${verifyFarkas(sys.sources, j.failCertificate, sys.variables.length)}`);
  }
}

hr();
line("4) Small-domain enumeration cross-checks the boundary");
{
  const store = new MeasurementStore();
  store.set("m", rat("0.5"), rat("0.5")); // m in [0, 1]
  const closed = constraint("closed", [{ measurement: "m", coeff: rat("1") }], "<=", rat("1"));
  const open = constraint("open", [{ measurement: "m", coeff: rat("1") }], "<", rat("1"));
  for (const c of [closed, open]) {
    const sys = compileSystem(store, [c]);
    const j = analyzeJoint(sys);
    const report = verifyByEnumeration(sys);
    const problems = crossCheck(sys, j, report);
    line(
      `  ${c.id} (m ${c.sense} 1, m in [0,1]): ${j.status}; ` +
        `enumeration vertices=${report.verticesChecked} points=${report.pointsTested}; ` +
        `cross-check ${problems.length === 0 ? "OK" : "MISMATCH: " + problems.join("; ")}`,
    );
  }
}

hr();
line("5) Budget exhaustion is reported as undecided");
{
  const store = new MeasurementStore();
  for (const id of ["x", "y", "z"]) store.set(id, rat("0"), rat("1"));
  const cs = [
    constraint("c1", [{ measurement: "x", coeff: rat("1") }, { measurement: "y", coeff: rat("1") }], "<=", rat("1")),
    constraint("c2", [{ measurement: "y", coeff: rat("1") }, { measurement: "z", coeff: rat("1") }], "<=", rat("1")),
    constraint("c3", [{ measurement: "x", coeff: rat("1") }, { measurement: "z", coeff: rat("-1") }], "<=", rat("1")),
    constraint("c4", [{ measurement: "x", coeff: rat("2") }, { measurement: "y", coeff: rat("-1") }, { measurement: "z", coeff: rat("1") }], ">=", rat("-1")),
  ];
  const sys = compileSystem(store, cs);
  const j = analyzeJoint(sys, { budget: { maxInequalities: 2 } });
  showJudgment("tiny budget", j);
}

hr();
line("6) Evidence: measurement updates revoke stale judgements, history kept");
{
  const store = new MeasurementStore();
  store.set("temp", rat("9"), rat("0.5"));
  const cs = new ConstraintSet();
  cs.add(constraint("limit", [{ measurement: "temp", coeff: rat("1") }], "<=", rat("10")));
  const evidence = new EvidenceStore<JointJudgment>(store, cs);
  const judge = () =>
    evidence.getOrCompute("joint", () => analyzeJoint(compileSystem(store, cs.all())));

  line(`  t0: temp=9+/-0.5   -> ${judge().value.status}`);
  store.set("temp", rat("10.5"), rat("0.5"));
  line(`  t1: temp=10.5+/-0.5 -> ${judge().value.status} (old certificate revoked)`);
  for (const e of evidence.history()) {
    line(`    epoch ${e.epoch}: ${e.event} ${e.key} -- ${e.reason}`);
  }
  const past = evidence.historyOf("joint")[0];
  line(`  preserved historical judgement: ${past.value.status} (revoked at epoch ${past.revokedAtEpoch})`);
}

hr();
line("7) Exact decimals: no Number transit, explicit rounding only");
{
  const a = rat("0.1").add(rat("0.2"));
  line(`  0.1 + 0.2 = ${a} (exact), not 0.30000000000000004`);
  const big = rat("9007199254740993").mul(rat("9007199254740993"));
  line(`  9007199254740993^2 = ${big} (exact BigInt)`);
  const v = rat("0.125");
  line(
    `  0.125 @2dp: floor=${v.toDecimal(2, "floor")} ceil=${v.toDecimal(2, "ceil")} ` +
      `halfUp=${v.toDecimal(2, "halfUp")} halfEven=${v.toDecimal(2, "halfEven")}`,
  );
}
