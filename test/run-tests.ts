import * as assert from "assert";
import { spawnSync } from "child_process";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { reduceScript, ReductionError } from "../src/reducer";
import { ReplayLog, ReplayLogEntry } from "../src/replay-log";
import { SimulatedCluster } from "../src/simulator";
import { ReplaySummary, ScriptFile, Step } from "../src/types";
import { validateScript } from "../src/validate";

const ROOT = path.join(__dirname, "..", "..");
const NODES = ["A", "B", "C"];
const SEED = 42;
const RECOVERY_MS = 150;

const validate = (steps: Step[]) => validateScript(steps, { nodes: NODES, recoveryMs: RECOVERY_MS });
const replay = (steps: Step[]): ReplaySummary =>
  new SimulatedCluster({ nodes: NODES, seed: SEED }).replay(steps);

type TestFn = () => void;
const tests: Array<[string, TestFn]> = [];
const test = (name: string, fn: TestFn): void => {
  tests.push([name, fn]);
};

function loadExample(): ScriptFile {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "examples", "failing-script.json"), "utf8"));
}

// ---------- validator ----------

test("validator accepts an executable script", () => {
  const steps: Step[] = [
    { type: "delay", ms: 200 },
    { type: "restart", node: "A" },
    { type: "delay", ms: 150 },
    { type: "packet-loss", node: "A", rate: 0.5 },
  ];
  const r = validate(steps);
  assert.strictEqual(r.ok, true, r.errors.join("; "));
});

test("validator rejects steps targeting a node still recovering from restart", () => {
  const r = validate([
    { type: "restart", node: "A" },
    { type: "delay", ms: 100 },
    { type: "packet-loss", node: "A", rate: 0.5 },
  ]);
  assert.strictEqual(r.ok, false);
  assert.ok(r.errors[0].includes("recovering"), r.errors[0]);
});

test("validator rejects double restart without recovery delay", () => {
  const r = validate([
    { type: "restart", node: "A" },
    { type: "restart", node: "A" },
  ]);
  assert.strictEqual(r.ok, false);
});

test("validator rejects malformed steps", () => {
  assert.strictEqual(validate([{ type: "packet-loss", node: "Z", rate: 0.5 }]).ok, false);
  assert.strictEqual(validate([{ type: "packet-loss", node: "A", rate: 1.5 }]).ok, false);
  assert.strictEqual(validate([{ type: "packet-loss", node: "A", rate: -0.1 }]).ok, false);
  assert.strictEqual(validate([{ type: "delay", ms: 0 }]).ok, false);
  assert.strictEqual(validate([{ type: "delay", ms: 10.5 }]).ok, false);
  assert.strictEqual(validate([{ type: "sleep", ms: 10 } as unknown as Step]).ok, false);
});

// ---------- simulator ----------

const MINIMAL_FAILING: Step[] = [
  { type: "delay", ms: 200 },
  { type: "packet-loss", node: "A", rate: 1.0 },
  { type: "delay", ms: 200 },
];

test("simulator reproduces the split-brain assertion failure", () => {
  const s = replay(MINIMAL_FAILING);
  assert.strictEqual(s.status, "fail");
  assert.ok(s.assertion && s.assertion.includes("split-brain"), s.assertion);
});

test("every single-step deletion of the minimal script passes", () => {
  for (let i = 0; i < MINIMAL_FAILING.length; i++) {
    const candidate = MINIMAL_FAILING.filter((_, j) => j !== i);
    const s = replay(candidate);
    assert.strictEqual(s.status, "pass", `deleting step ${i} should pass`);
  }
});

test("simulator reports invalid scripts instead of running them", () => {
  const s = replay([
    { type: "restart", node: "A" },
    { type: "packet-loss", node: "A", rate: 0.5 },
  ]);
  assert.strictEqual(s.status, "invalid");
  assert.ok(s.invalidReason && s.invalidReason.includes("recovering"));
});

test("replays are deterministic for the same seed", () => {
  assert.deepStrictEqual(replay(MINIMAL_FAILING), replay(MINIMAL_FAILING));
});

// ---------- reducer ----------

test("reducer shrinks the example script to a 1-minimal failure", () => {
  const example = loadExample();
  const log = new ReplayLog();
  const report = reduceScript(example.steps, { harness: new SimulatedCluster({ nodes: NODES, seed: SEED }), validate, log });

  assert.ok(report.reducedLength < report.originalLength, "should remove steps");
  assert.strictEqual(replay(report.reduced).status, "fail", "reduced script must still fail");
  assert.strictEqual(validate(report.reduced).ok, true, "reduced script must be valid");
  assert.strictEqual(report.verifiedOneMinimal, true);

  // Independent 1-minimality check: no single-step deletion reproduces.
  for (let i = 0; i < report.reduced.length; i++) {
    const candidate = report.reduced.filter((_, j) => j !== i);
    if (!validate(candidate).ok) continue; // invalid deletions are not "failures"
    assert.strictEqual(replay(candidate).status, "pass", `deleting step ${i} must not fail`);
  }

  // Every replay was logged with a summary.
  assert.strictEqual(log.count, report.totalReplays);
  for (const e of log.entries) {
    assert.ok(e.summary && typeof e.summary.status === "string");
    assert.strictEqual(e.status, e.summary.status);
    assert.ok(e.scriptHash.length > 0);
  }
  const want = { fail: 0, pass: 0, invalid: 0 } as Record<string, number>;
  for (const e of log.entries) want[e.status] += 1;
  assert.deepStrictEqual(report.counts, want);
});

test("reducer never accepts invalid scripts as reductions", () => {
  // Harness "fails" only while the script contains both a restart and a
  // packet-loss step, so the only way to shrink further would be deleting
  // the recovery delay — which is invalid and must not be accepted.
  const steps: Step[] = [
    { type: "restart", node: "C" },
    { type: "delay", ms: 200 },
    { type: "packet-loss", node: "C", rate: 0.5 },
  ];
  const harness = {
    replay(candidate: Step[]): ReplaySummary {
      const hasRestart = candidate.some((s) => s.type === "restart");
      const hasLoss = candidate.some((s) => s.type === "packet-loss");
      return {
        status: hasRestart && hasLoss ? "fail" : "pass",
        simulatedMs: 0,
        events: 0,
        finalLeaders: [],
        finalTerms: {},
      };
    },
  };
  const log = new ReplayLog();
  const report = reduceScript(steps, { harness, validate, log });

  assert.strictEqual(report.reducedLength, 3, "no valid deletion exists, script must be kept");
  assert.strictEqual(validate(report.reduced).ok, true);
  assert.ok(report.counts.invalid >= 1, "invalid candidate must have been attempted and logged");
  const invalidEntries = log.entries.filter((e) => e.status === "invalid");
  assert.ok(invalidEntries.length >= 1);
  assert.ok(invalidEntries[0].summary.invalidReason);
});

test("reducer refuses input that does not reproduce the failure", () => {
  assert.throws(
    () =>
      reduceScript([{ type: "delay", ms: 100 }], {
        harness: new SimulatedCluster({ nodes: NODES, seed: SEED }),
        validate,
      }),
    (err: Error) => err instanceof ReductionError
  );
});

// ---------- CLI ----------

test("CLI reduce writes a 1-minimal script and a complete replay log", () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "psr-"));
  const out = path.join(tmp, "reduced.json");
  const logPath = path.join(tmp, "replays.jsonl");
  const cli = path.join(ROOT, "dist", "cli.js");
  const examplePath = path.join(ROOT, "examples", "failing-script.json");

  const reduce = spawnSync(
    process.execPath,
    [cli, "reduce", "--script", examplePath, "--out", out, "--log", logPath],
    { encoding: "utf8" }
  );
  assert.strictEqual(reduce.status, 0, reduce.stderr);
  assert.ok(reduce.stdout.includes("1-minimal: verified"), reduce.stdout);

  const reduced: ScriptFile = JSON.parse(fs.readFileSync(out, "utf8"));
  assert.strictEqual(replay(reduced.steps).status, "fail");

  const lines = fs.readFileSync(logPath, "utf8").trim().split("\n");
  assert.ok(lines.length > 0);
  const entries: ReplayLogEntry[] = lines.map((l) => JSON.parse(l));
  entries.forEach((e, i) => {
    assert.strictEqual(e.seq, i + 1);
    assert.ok(["fail", "pass", "invalid"].includes(e.status));
    assert.ok(e.summary);
  });

  const replayCmd = spawnSync(process.execPath, [cli, "replay", "--script", out], { encoding: "utf8" });
  assert.strictEqual(replayCmd.status, 0, replayCmd.stderr);
  assert.ok(replayCmd.stdout.includes('"status": "fail"'));
});

test("CLI reduce exits non-zero when the input does not fail", () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "psr-"));
  const passing = path.join(tmp, "passing.json");
  fs.writeFileSync(
    passing,
    JSON.stringify({ nodes: NODES, seed: SEED, steps: [{ type: "delay", ms: 100 }] })
  );
  const cli = path.join(ROOT, "dist", "cli.js");
  const res = spawnSync(process.execPath, [cli, "reduce", "--script", passing], { encoding: "utf8" });
  assert.strictEqual(res.status, 2);
  assert.ok(res.stderr.includes("does not reproduce"), res.stderr);
});

// ---------- runner ----------

let failures = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (err) {
    failures += 1;
    console.error(`FAIL ${name}`);
    console.error(err instanceof Error ? err.stack : err);
  }
}
console.log(`${tests.length - failures}/${tests.length} tests passed`);
process.exit(failures === 0 ? 0 : 1);
