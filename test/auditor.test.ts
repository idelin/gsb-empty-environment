import { audit, AuditReport } from "../src/auditor";
import { BuildSpec, classifyDifference } from "../src/model";
import { traceRun } from "../src/run";
import { spec as demoSpec } from "../examples/demo.build";
import { assert, assertEqual, assertThrows, test } from "./harness";

function isOneMinimal(report: AuditReport, spec: BuildSpec): boolean {
  for (const ce of report.counterexamples) {
    for (const d of ce.diffInputs) {
      const bAligned = { ...ce.b, [d]: ce.a[d]! };
      const out = traceRun(spec, bAligned);
      if (classifyDifference(ce.outputA, out) === ce.kind) return false;
      const aAligned = { ...ce.a, [d]: ce.b[d]! };
      const out2 = traceRun(spec, aAligned);
      if (classifyDifference(out2, ce.outputB) === ce.kind) return false;
    }
  }
  return true;
}

test("sound spec: key covers every effective input", () => {
  const spec: BuildSpec = {
    name: "sound",
    domain: { "env:A": ["0", "1"], "env:B": ["0", "1"] },
    keyInputs: ["env:A", "env:B"],
    build: (env) => ({
      artifact: `a=${env["env:A"]},b=${env["env:B"]}`,
      log: "quiet",
    }),
  };
  const report = audit(spec);
  assertEqual(report.counterexamples.length, 0, "no counterexamples");
  assertEqual(report.environmentCount, 4, "enumerated all environments");
  assertEqual(report.keyCount, 4, "all keys distinct");
});

test("undeclared artifact input yields a minimal artifact-affecting counterexample", () => {
  const spec: BuildSpec = {
    name: "unsound-artifact",
    domain: { "env:A": ["0", "1"], "tool:x": ["1.0", "2.0"] },
    keyInputs: ["env:A"],
    build: (env) => ({
      artifact: `a=${env["env:A"]}|x=${env["tool:x"]}`,
      log: "quiet",
    }),
  };
  const report = audit(spec);
  const ces = report.counterexamples.filter((c) => c.kind === "artifact-affecting");
  assert(ces.length > 0, "expected artifact-affecting counterexamples");
  for (const ce of ces) {
    assertEqual(ce.diffInputs, ["tool:x"], "minimal counterexample differs in exactly one input");
  }
  assertEqual(report.undeclaredEffectiveInputs, ["tool:x"], "tool:x flagged missing from key");
  assert(isOneMinimal(report, spec), "all counterexamples are 1-minimal");
});

test("log-only differences are classified separately from artifact differences", () => {
  const spec: BuildSpec = {
    name: "log-only",
    domain: { "env:A": ["0", "1"], "env:NOISY": ["0", "1"] },
    keyInputs: ["env:A"],
    build: (env) => ({
      artifact: `a=${env["env:A"]}`,
      log: `noisy=${env["env:NOISY"]}`,
    }),
  };
  const report = audit(spec);
  assertEqual(
    report.counterexamples.filter((c) => c.kind === "artifact-affecting").length,
    0,
    "no artifact-affecting counterexamples",
  );
  const logOnly = report.counterexamples.filter((c) => c.kind === "log-only");
  assert(logOnly.length > 0, "expected log-only counterexamples");
  for (const ce of logOnly) {
    assertEqual(ce.outputA.artifact, ce.outputB.artifact, "artifact identical");
    assert(ce.outputA.log !== ce.outputB.log, "log differs");
  }
  assertEqual(report.inputEffects["env:NOISY"], "log-only", "effect classified as log-only");
});

test("conditional reads: counterexample only under the guarding key value", () => {
  const spec: BuildSpec = {
    name: "conditional",
    domain: {
      "env:MODE": ["dev", "prod"],
      "file:extra": ["absent", "present"],
    },
    keyInputs: ["env:MODE"],
    build: (env) => {
      let artifact = `mode=${env["env:MODE"]}`;
      if (env["env:MODE"] === "prod" && "file:extra" in env) {
        artifact += `|extra=${env["file:extra"]}`;
      }
      return { artifact, log: "quiet" };
    },
  };
  const report = audit(spec);
  const ces = report.counterexamples.filter((c) => c.kind === "artifact-affecting");
  assert(ces.length > 0, "expected counterexamples");
  for (const ce of ces) {
    assertEqual(ce.diffInputs, ["file:extra"], "minimal diff is the conditionally read file");
    assertEqual(ce.key["env:MODE"], "prod", "counterexample only exists in the prod branch");
  }
  assert(isOneMinimal(report, spec), "all counterexamples are 1-minimal");
});

test("minimization shrinks multi-input candidates to 1-minimal pairs", () => {
  const spec: BuildSpec = {
    name: "multi",
    domain: { "x": ["0", "1"], "y": ["0", "1"], "z": ["0", "1"] },
    keyInputs: [],
    build: (env) => ({
      artifact: `${env["x"]}${env["y"]}${env["z"]}`,
      log: "quiet",
    }),
  };
  const report = audit(spec);
  assert(report.counterexamples.length > 0, "expected counterexamples");
  for (const ce of report.counterexamples) {
    assertEqual(ce.diffInputs.length, 1, "minimized to a single differing input");
  }
});

test("declared-but-ineffective and never-read inputs are reported", () => {
  const spec: BuildSpec = {
    name: "noise",
    domain: {
      "env:USED": ["0", "1"],
      "env:IN_KEY_BUT_DEAD": ["0", "1"],
      "env:NEVER_READ": ["0", "1"],
    },
    keyInputs: ["env:USED", "env:IN_KEY_BUT_DEAD"],
    build: (env) => ({ artifact: `u=${env["env:USED"]}`, log: "quiet" }),
  };
  const report = audit(spec);
  assertEqual(report.unusedKeyInputs, ["env:IN_KEY_BUT_DEAD"], "dead key input detected");
  assertEqual(
    report.neverReadInputs,
    ["env:IN_KEY_BUT_DEAD", "env:NEVER_READ"],
    "never-read inputs detected",
  );
  assertEqual(report.counterexamples.length, 0, "dead inputs never produce counterexamples");
});

test("enumeration limit is enforced", () => {
  const spec: BuildSpec = {
    name: "huge",
    domain: Object.fromEntries(
      Array.from({ length: 20 }, (_, i) => [`env:V${i}`, ["0", "1"]]),
    ),
    keyInputs: [],
    build: () => ({ artifact: "x", log: "y" }),
  };
  assertThrows(() => audit(spec, { maxEnvironments: 1000 }), /too large/i, "limit enforced");
});

test("demo spec: expected findings", () => {
  const report = audit(demoSpec);
  const kinds = new Set(report.counterexamples.map((c) => c.kind));
  assert(kinds.has("artifact-affecting"), "demo has artifact-affecting counterexamples");
  assert(kinds.has("log-only"), "demo has log-only counterexamples");
  assertEqual(
    [...report.undeclaredEffectiveInputs].sort(),
    ["env:CI", "env:VERBOSE", "file:terser.config.json", "tool:node", "tool:terser"],
    "all undeclared effective inputs found",
  );
  assertEqual(report.unusedKeyInputs, ["env:COLOR"], "env:COLOR is dead weight in the key");
  assertEqual(
    [...report.neverReadInputs].sort(),
    ["env:COLOR", "env:LEGACY_FLAG"],
    "never-read inputs detected",
  );
  assert(isOneMinimal(report, demoSpec), "demo counterexamples are 1-minimal");
});
