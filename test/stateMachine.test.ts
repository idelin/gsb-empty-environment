import { test } from "node:test";
import assert from "node:assert/strict";
import { CanaryStateMachine } from "../src/stateMachine.js";

const config = {
  stages: [5, 25, 50, 100],
  passesPerStage: 2,
  maxConsecutiveInconclusive: 2,
};

test("consecutive passes promote through all stages to completion", () => {
  const sm = new CanaryStateMachine(config);
  assert.equal(sm.state.trafficPercent, 5);
  assert.equal(sm.state.status, "PROMOTING");

  sm.step("PASS");
  assert.equal(sm.state.trafficPercent, 5); // 1 of 2 passes
  sm.step("PASS");
  assert.equal(sm.state.trafficPercent, 25); // promoted
  assert.equal(sm.state.consecutivePasses, 0);

  sm.step("PASS");
  sm.step("PASS");
  assert.equal(sm.state.trafficPercent, 50);

  sm.step("PASS");
  sm.step("PASS");
  assert.equal(sm.state.trafficPercent, 100);
  assert.equal(sm.state.status, "PROMOTING");

  sm.step("PASS");
  sm.step("PASS");
  assert.equal(sm.state.status, "COMPLETED");
  assert.equal(sm.state.trafficPercent, 100);
});

test("a single FAIL rolls back to 0% immediately", () => {
  const sm = new CanaryStateMachine(config);
  sm.step("PASS");
  sm.step("PASS"); // -> 25%
  sm.step("PASS");
  const r = sm.step("FAIL");
  assert.equal(r.status, "ROLLED_BACK");
  assert.equal(r.trafficPercent, 0);
  assert.equal(r.stageIndex, 0);
});

test("consecutive INCONCLUSIVE beyond the limit rolls back", () => {
  const sm = new CanaryStateMachine(config);
  sm.step("INCONCLUSIVE");
  assert.equal(sm.state.status, "PROMOTING");
  sm.step("INCONCLUSIVE");
  assert.equal(sm.state.status, "PROMOTING"); // at limit, still alive
  const r = sm.step("INCONCLUSIVE"); // exceeds limit
  assert.equal(r.status, "ROLLED_BACK");
  assert.equal(r.trafficPercent, 0);
});

test("a PASS resets the inconclusive counter; inconclusive resets pass streak", () => {
  const sm = new CanaryStateMachine(config);
  sm.step("INCONCLUSIVE");
  sm.step("INCONCLUSIVE");
  sm.step("PASS"); // resets inconclusive streak, 1 pass
  assert.equal(sm.state.consecutiveInconclusive, 0);
  assert.equal(sm.state.consecutivePasses, 1);
  sm.step("INCONCLUSIVE"); // resets pass streak
  assert.equal(sm.state.consecutivePasses, 0);
  sm.step("PASS");
  sm.step("PASS");
  assert.equal(sm.state.trafficPercent, 25);
});

test("terminal states ignore further verdicts", () => {
  const sm = new CanaryStateMachine(config);
  sm.step("FAIL");
  const s1 = sm.step("PASS");
  assert.equal(s1.status, "ROLLED_BACK");
  assert.equal(s1.trafficPercent, 0);
});

test("config validation", () => {
  assert.throws(
    () => new CanaryStateMachine({ ...config, stages: [] }),
    /at least one/,
  );
  assert.throws(
    () => new CanaryStateMachine({ ...config, passesPerStage: 0 }),
    /passesPerStage/,
  );
});
