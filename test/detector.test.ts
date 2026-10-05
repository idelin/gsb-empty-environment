import assert from "node:assert/strict";
import { test } from "node:test";
import { congestedDelay, constantDelay, lognormalDelay, mulberry32, uniformDelay } from "../src/random.ts";
import { Simulation } from "../src/simulation.ts";

test("健康节点：低抖动下无疑似、无移除", () => {
  const rng = mulberry32(42);
  const sim = new Simulation({ delaySampler: uniformDelay(rng, 5, 50) });
  sim.addNode("n1");
  const s = sim.run(60_000);
  assert.equal(s.suspicions, 0);
  assert.equal(s.falsePositives, 0);
  assert.equal(s.trueRemovals, 0);
});

test("短暂暂停：先怀疑，收到当前代次心跳后恢复，不移除", () => {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.schedulePause("n1", 10_000, 3_500);
  const s = sim.run(30_000);
  assert.equal(s.suspicions, 1);
  assert.equal(s.falseSuspicions, 1);
  assert.equal(s.falsePositives, 0);
  assert.equal(s.trueRemovals, 0);
});

test("长时间暂停：超过等待窗口被移除，计为误判", () => {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.schedulePause("n1", 10_000, 8_000);
  const s = sim.run(30_000);
  assert.equal(s.falsePositives, 1);
  assert.equal(s.trueRemovals, 0);
  assert.ok(s.postRemovalHeartbeats > 0);
});

test("干净崩溃：移除且检测延迟符合理论值", () => {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.scheduleCrash("n1", 20_000);
  const s = sim.run(40_000);
  assert.equal(s.trueRemovals, 1);
  assert.equal(s.falsePositives, 0);
  assert.equal(s.detectionLatencyCount, 1);
  assert.ok(Math.abs(s.detectionLatencyAvgMs - 4020) < 1e-6);
});

test("旧心跳：旧代次证据被忽略，不阻止对当前代次崩溃的移除", () => {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.schedulePause("n1", 10_000, 3_500);
  sim.scheduleRestart("n1", 13_500);
  sim.scheduleCrash("n1", 16_000);
  sim.injectHeartbeat("n1", 1, 9_000, 19_500);
  sim.injectHeartbeat("n1", 1, 9_500, 19_800);
  const s = sim.run(40_000);
  assert.equal(s.staleHeartbeatsIgnored, 2);
  assert.equal(s.falseSuspicions, 1);
  assert.equal(s.trueRemovals, 1);
  assert.equal(s.falsePositives, 0);
  assert.ok(Math.abs(s.detectionLatencyAvgMs - 4020) < 1e-6);
});

test("长尾延迟：自适应窗口的误判不多于固定窗口", () => {
  const runOnce = (adaptive: boolean) => {
    const rng = mulberry32(777);
    const sim = new Simulation({
      delaySampler: constantDelay(10),
      delaySamplerFor: () =>
        congestedDelay(rng, {
          episodeProbability: 0.03,
          episodeMinMs: 2_000,
          episodeMaxMs: 4_000,
          congestedMinMs: 1_500,
          congestedMaxMs: 3_000,
          baseMinMs: 5,
          baseMaxMs: 80,
        }),
      detectorConfig: adaptive ? { adaptive: true } : { adaptive: false, fixedWindowMs: 300 },
    });
    for (let i = 1; i <= 8; i += 1) sim.addNode(`n${i}`);
    return sim.run(120_000);
  };
  const adaptive = runOnce(true);
  const fixed = runOnce(false);
  assert.equal(adaptive.falsePositives, 0);
  assert.ok(adaptive.suspicions > 0);
  assert.ok(fixed.falsePositives > adaptive.falsePositives);
});

test("可复现：相同随机种子产生相同指标", () => {
  const runOnce = () => {
    const rng = mulberry32(2024);
    const sim = new Simulation({ delaySampler: lognormalDelay(rng, Math.log(40), 1.8) });
    for (let i = 1; i <= 4; i += 1) sim.addNode(`n${i}`);
    return sim.run(60_000);
  };
  assert.deepEqual(runOnce(), runOnce());
});
