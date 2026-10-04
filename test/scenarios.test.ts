import { test } from "node:test";
import assert from "node:assert/strict";
import {
  baseline,
  longTailDelay,
  pauseBursts,
  realCrash,
  staleHeartbeats,
  type Scenario,
} from "../src/scenarios.ts";

function run(sc: Scenario) {
  const { sim, durationMs } = sc.create();
  sim.run(durationMs);
  sim.finish();
  return sim;
}

test("baseline: no false positives and no detections without faults", () => {
  const sim = run(baseline());
  assert.equal(sim.metrics.falseSuspects, 0);
  assert.equal(sim.metrics.falseRemovals, 0);
  assert.equal(sim.metrics.detectionLatenciesMs.length, 0);
  assert.equal(sim.metrics.missedDetections, 0);
});

test("pause bursts: short pause is tolerated, medium recovers, long causes false removal + rejoin", () => {
  const sim = run(pauseBursts());
  assert.equal(sim.metrics.falseSuspects, 2); // 250ms and 2000ms pauses
  assert.equal(sim.metrics.falseRemovals, 1); // only the 2000ms pause
  assert.equal(sim.metrics.recoveries, 1);
  assert.equal(sim.metrics.rejoins, 1);
  assert.equal(sim.metrics.detectionLatenciesMs.length, 0); // no real crash
});

test("long-tail delay: suspicion happens but false removals stay bounded", () => {
  const sim = run(longTailDelay());
  assert.equal(sim.metrics.falseSuspects, 7);
  assert.equal(sim.metrics.falseRemovals, 4);
  assert.equal(sim.metrics.detectionLatenciesMs.length, 0);
  assert.equal(sim.metrics.missedDetections, 0);
});

test("stale heartbeats: old-epoch and pre-removal evidence never revive the node", () => {
  const sim = run(staleHeartbeats());
  assert.equal(sim.detector.staleIgnored, 4);
  assert.equal(sim.metrics.falseSuspects, 0);
  assert.equal(sim.metrics.falseRemovals, 0);
  assert.equal(sim.metrics.recoveries, 1); // new-epoch evidence recovered the suspect
  assert.equal(sim.metrics.detectionLatenciesMs.length, 1); // only the permanent crash
  const latency = sim.metrics.detectionLatenciesMs[0];
  assert.ok(latency >= 300 && latency <= 1000, `latency ${latency}ms out of expected range`);
});

test("real crash: detection latency is measured separately from false positives", () => {
  const sim = run(realCrash());
  assert.equal(sim.metrics.falseSuspects, 0);
  assert.equal(sim.metrics.falseRemovals, 0);
  assert.equal(sim.metrics.detectionLatenciesMs.length, 1);
  const latency = sim.metrics.detectionLatenciesMs[0];
  assert.ok(latency >= 300 && latency <= 1000, `latency ${latency}ms out of expected range`);
  assert.equal(sim.metrics.missedDetections, 0);
});
