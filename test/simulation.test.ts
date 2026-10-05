import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runSimulation, DEFAULT_SIM_CONFIG, type SimConfig } from "../src/simulation.ts";
import { auditLedgers } from "../src/audit.ts";
import { Ledger } from "../src/ledger.ts";

function tmp(): string {
  return mkdtempSync(join(tmpdir(), "quota-sim-"));
}

function chaosConfig(outDir: string, seed = 7): SimConfig {
  return {
    ...DEFAULT_SIM_CONFIG,
    seed,
    outDir,
    lossRate: 0.15,
    dupRate: 0.1,
    partitions: [
      { from: 120, to: 139 }, // mid-window partition
      { from: 243, to: 258 }, // partition straddling the window boundary at 250
    ],
    restartAt: { A: 170, B: 300 },
  };
}

test("chaos simulation: audit finds no invariant violations", () => {
  const dir = join(tmp(), "run");
  const config = chaosConfig(dir);
  runSimulation(config);
  const report = auditLedgers({
    dir,
    nodeIds: config.nodeIds,
    windowQuota: config.windowQuota,
    windowLength: config.windowLength,
  });
  assert.deepEqual(report.violations, []);
  for (const w of report.windows) {
    assert.ok(w.consumed <= w.quota, `window ${w.windowId} consumed ${w.consumed} <= ${w.quota}`);
    assert.ok(w.consumed <= w.issued, `window ${w.windowId} consumed <= issued`);
    assert.equal(w.idle, w.quota - w.consumed);
  }
  assert.ok(report.totalAccepted > 0, "some requests were accepted");
  assert.ok(report.conflicts.length > 0, "conflict injection exercised");
  assert.ok(report.rejections.some((r) => r.reason === "insufficient_quota"));
  assert.ok(report.rejections.some((r) => r.reason === "window_quota_exhausted"));
});

test("nodes keep spending borrowed quota during a network partition", () => {
  const dir = join(tmp(), "run");
  const config = chaosConfig(dir, 7);
  runSimulation(config);
  const during = [120, 139];
  let count = 0;
  for (const nodeId of config.nodeIds) {
    for (const event of Ledger.read(join(dir, `node-${nodeId}.ledger.jsonl`))) {
      if (event.ev === "consume_accepted" && event.tick >= during[0] && event.tick <= during[1]) {
        count++;
      }
    }
  }
  assert.ok(count > 0, "consumes accepted while the network was partitioned");
});

test("grants delivered across a window boundary arrive late and are returned, not spent", () => {
  const dir = join(tmp(), "run");
  const config = chaosConfig(dir, 7);
  runSimulation(config);
  let late = 0;
  let spentLate = 0;
  for (const nodeId of config.nodeIds) {
    const events = Ledger.read(join(dir, `node-${nodeId}.ledger.jsonl`));
    const lateGrantIds = new Set<string>();
    for (const event of events) {
      if (event.ev === "late_grant") {
        late++;
        lateGrantIds.add((event.grant as { grantId: string }).grantId);
      }
    }
    for (const event of events) {
      if (event.ev === "consume_accepted") {
        for (const alloc of event.allocations as Array<{ grantId: string }>) {
          if (lateGrantIds.has(alloc.grantId)) spentLate++;
        }
      }
    }
  }
  assert.ok(late > 0, "partition across window boundary produced late grants");
  assert.equal(spentLate, 0, "late grants were never spent");
});

test("replay is deterministic: identical seeds produce byte-identical ledgers", () => {
  const dirA = join(tmp(), "run");
  const dirB = join(tmp(), "run");
  runSimulation(chaosConfig(dirA, 42));
  runSimulation(chaosConfig(dirB, 42));
  for (const file of ["center.ledger.jsonl", "node-A.ledger.jsonl", "node-B.ledger.jsonl"]) {
    assert.equal(readFileSync(join(dirB, file), "utf8"), readFileSync(join(dirA, file), "utf8"), file);
  }
});

test("every accepted request id is counted exactly once across restarts", () => {
  const dir = join(tmp(), "run");
  const config = chaosConfig(dir, 11);
  runSimulation(config);
  const seen = new Map<string, number>();
  for (const nodeId of config.nodeIds) {
    for (const event of Ledger.read(join(dir, `node-${nodeId}.ledger.jsonl`))) {
      if (event.ev === "consume_accepted") {
        const key = `${nodeId}/${event.requestId}`;
        seen.set(key, (seen.get(key) ?? 0) + 1);
      }
    }
  }
  const report = auditLedgers({
    dir,
    nodeIds: config.nodeIds,
    windowQuota: config.windowQuota,
    windowLength: config.windowLength,
  });
  assert.equal(report.totalAccepted, seen.size, "audit counts each accepted id once");
  for (const [key, n] of seen) {
    assert.ok(n >= 1, key);
  }
});
