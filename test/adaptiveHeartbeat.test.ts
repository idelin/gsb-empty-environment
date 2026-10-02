import { test } from "node:test";
import assert from "node:assert/strict";
import { AdaptiveHeartbeatScheduler } from "../src/adaptiveHeartbeat.ts";

test("网络稳定时间隔按衰减因子上调，直至上限", () => {
  const s = new AdaptiveHeartbeatScheduler({
    initialIntervalMs: 1000,
    maxIntervalMs: 5000,
    decayFactor: 1.5,
  });
  const seen: number[] = [s.intervalMs];
  for (let i = 0; i < 20; i++) {
    seen.push(s.observe(1000, 1000, 100)); // 完全贴合分布
  }
  assert.equal(seen[1], 1500);
  assert.equal(seen[2], 2250);
  assert.equal(s.intervalMs, 5000, "应收敛到 maxIntervalMs");
});

test("观测到异常抖动时间隔快速收缩，直至下限", () => {
  const s = new AdaptiveHeartbeatScheduler({
    initialIntervalMs: 4000,
    minIntervalMs: 200,
    shrinkFactor: 0.5,
    anomalyZScore: 3,
  });
  // 偏离均值 5 个标准差 → 异常
  const next = s.observe(1000 + 5 * 100, 1000, 100);
  assert.equal(next, 2000);
  s.observe(1600, 1000, 100);
  s.observe(1600, 1000, 100);
  s.observe(1600, 1000, 100);
  s.observe(1600, 1000, 100);
  assert.equal(s.intervalMs, 200, "应收缩到 minIntervalMs");
});

test("偏离在正常方差内不触发收缩", () => {
  const s = new AdaptiveHeartbeatScheduler({ initialIntervalMs: 1000 });
  // 偏离 2 个标准差（< 3σ）→ 视为正常抖动，继续衰减上调
  const next = s.observe(1000 + 2 * 100, 1000, 100);
  assert.ok(next > 1000, "正常方差内的抖动不应收缩间隔");
});

test("escalate 立即降到最小间隔", () => {
  const s = new AdaptiveHeartbeatScheduler({
    initialIntervalMs: 8000,
    minIntervalMs: 200,
  });
  assert.equal(s.escalate(), 200);
});
