import { test } from "node:test";
import assert from "node:assert/strict";
import { FailureDetector, type MonitorConfig } from "../src/monitor.ts";
import type { Heartbeat } from "../src/types.ts";

const cfg: MonitorConfig = {
  heartbeatIntervalMs: 100,
  suspectThreshold: 3,
  windowCapacity: 8,
  marginK: 3,
  minSuspectTimeoutMs: 150,
  maxSuspectTimeoutMs: 2000,
};

function hb(epoch: number, seq: number, sentAt: number): Heartbeat {
  return { nodeId: "n1", epoch, seq, sentAt };
}

test("suspects only after enough consecutive missed heartbeats", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(1, 0, 0), 10);
  det.evaluate(300); // missed = 2
  assert.equal(det.stateOf("n1"), "ALIVE");
  det.evaluate(310); // missed = 3
  assert.equal(det.stateOf("n1"), "SUSPECTED");
});

test("recovers when current-epoch evidence arrives while suspected", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(1, 0, 0), 10);
  det.evaluate(310);
  assert.equal(det.stateOf("n1"), "SUSPECTED");
  det.onHeartbeat(hb(1, 1, 300), 320);
  assert.equal(det.stateOf("n1"), "ALIVE");
});

test("removes only after the adaptive timeout elapses", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(1, 0, 0), 0);
  det.evaluate(300); // SUSPECTED at 300; no samples -> timeout = 3 * interval = 300
  assert.equal(det.stateOf("n1"), "SUSPECTED");
  det.evaluate(599); // 299 < 300
  assert.equal(det.stateOf("n1"), "SUSPECTED");
  det.evaluate(600); // 300 >= 300
  assert.equal(det.stateOf("n1"), "REMOVED");
});

test("stale heartbeats from an older epoch are ignored", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(2, 0, 0), 0);
  det.evaluate(300);
  assert.equal(det.stateOf("n1"), "SUSPECTED");
  det.onHeartbeat(hb(1, 9, 100), 320);
  assert.equal(det.stateOf("n1"), "SUSPECTED");
  assert.equal(det.staleIgnored, 1);
});

test("evidence older than the removal decision cannot revive a node", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(1, 0, 0), 0);
  det.evaluate(300);
  det.evaluate(600); // REMOVED at 600
  assert.equal(det.stateOf("n1"), "REMOVED");
  det.onHeartbeat(hb(1, 1, 400), 700); // sent before removal -> old evidence
  assert.equal(det.stateOf("n1"), "REMOVED");
  assert.equal(det.staleIgnored, 1);
  det.onHeartbeat(hb(1, 2, 650), 720); // sent after removal -> fresh evidence
  assert.equal(det.stateOf("n1"), "ALIVE");
});

test("a newer epoch re-registers the node even after removal", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(1, 0, 0), 0);
  det.evaluate(300);
  det.evaluate(600);
  assert.equal(det.stateOf("n1"), "REMOVED");
  det.onHeartbeat(hb(2, 0, 650), 700);
  assert.equal(det.stateOf("n1"), "ALIVE");
});

test("adaptive window widens the timeout with delay variance and clamps to max", () => {
  const det = new FailureDetector(cfg);
  det.onHeartbeat(hb(1, 0, 0), 100); // registration, no sample
  det.onHeartbeat(hb(1, 1, 100), 300); // delay 200
  det.onHeartbeat(hb(1, 2, 200), 300); // delay 100
  det.onHeartbeat(hb(1, 3, 300), 500); // delay 200
  det.onHeartbeat(hb(1, 4, 400), 500); // delay 100
  // samples {200,100,200,100}: mean 150, stddev 50 -> 100 + 150 + 3*50 = 400
  assert.equal(det.suspectTimeoutMs("n1"), 400);

  const det2 = new FailureDetector(cfg);
  det2.onHeartbeat(hb(1, 0, 0), 0);
  det2.onHeartbeat(hb(1, 1, 0), 5000); // delay 5000 -> est 5100 -> clamped to 2000
  assert.equal(det2.suspectTimeoutMs("n1"), 2000);
});
