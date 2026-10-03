import { test } from "node:test";
import assert from "node:assert/strict";
import { SlidingWindowConditionIntegrator } from "../src/sliding-window-integrator.ts";

const WINDOW_MS = 100_000; // 100 秒窗口
const OVER_TEMP = (value: number): boolean => value > 80;

/** 验收场景：t=10s 升至 85，t=40s 降至 75（时间单位统一换算为毫秒）。 */
function acceptanceIntegrator(): SlidingWindowConditionIntegrator {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  integrator.addSample(10_000, 85); // t=10s 进入告警
  integrator.addSample(40_000, 75); // t=40s 退出告警
  return integrator;
}

test("验收边界：t=50s 时累计超温时间为 30 秒", () => {
  const integrator = acceptanceIntegrator();
  assert.equal(integrator.accumulatedMs(50_000), 30_000);
});

test("验收边界：t=10s 滑出窗口后超温时间平滑衰减，t=140s 完全归零", () => {
  const integrator = acceptanceIntegrator();
  // t=110s：窗口 [10s, 110s]，告警段 [10s,40s] 恰好完整保留
  assert.equal(integrator.accumulatedMs(110_000), 30_000);
  // t=120s：窗口 [20s, 120s]，t=10s 已滑出，剩余 [20s,40s] = 20s
  assert.equal(integrator.accumulatedMs(120_000), 20_000);
  // t=130s：窗口 [30s, 130s]，剩余 [30s,40s] = 10s
  assert.equal(integrator.accumulatedMs(130_000), 10_000);
  // t=140s：窗口 [40s, 140s]，告警段完全滑出，归零
  assert.equal(integrator.accumulatedMs(140_000), 0);
  // 更晚时刻保持为零，不会出现负值或回弹
  assert.equal(integrator.accumulatedMs(1_000_000), 0);
});

test("平滑性：滑出阶段随时间线性递减，无跳变", () => {
  const integrator = acceptanceIntegrator();
  let previous = integrator.accumulatedMs(110_000);
  for (let nowMs = 110_001; nowMs <= 140_000; nowMs += 1) {
    const current = integrator.accumulatedMs(nowMs);
    assert.ok(previous - current <= 1, "每毫秒最多扣减 1ms，严格平滑");
    previous = current;
  }
  assert.equal(previous, 0);
});

test("精确扣减：非整数毫秒突变点不产生数值积分漂移", () => {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  integrator.addSample(0, 20);
  integrator.addSample(10_000.25, 85); // 进入告警
  integrator.addSample(40_000.75, 75); // 退出告警
  // 告警时长恰为 30000.5ms
  assert.equal(integrator.accumulatedMs(50_000), 30_000.5);
  // 突变点滑出时按精确时间戳扣减
  assert.equal(
    integrator.accumulatedMs(110_000.25 + 10_000),
    30_000.5 - 10_000,
  );
});

test("无漂移：对同一时刻重复查询结果恒定，不随查询次数累积", () => {
  const integrator = acceptanceIntegrator();
  const first = integrator.accumulatedMs(120_000);
  for (let i = 0; i < 10_000; i += 1) {
    assert.equal(integrator.accumulatedMs(120_000), first);
  }
});

test("多段告警：窗口内多个满足区间分别精确求交后求和", () => {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  integrator.addSample(0, 20);
  integrator.addSample(5_000, 90); // 告警段 1：[5s, 15s)
  integrator.addSample(15_000, 70);
  integrator.addSample(60_000, 88); // 告警段 2：[60s, 90s)
  integrator.addSample(90_000, 60);
  assert.equal(integrator.accumulatedMs(100_000), 10_000 + 30_000);
  // t=105s：窗口 [5s,105s]，段 1 起点恰在边界，仍完整计入
  assert.equal(integrator.accumulatedMs(105_000), 10_000 + 30_000);
  // t=120s：窗口 [20s,120s]，段 1 完全滑出，只剩段 2
  assert.equal(integrator.accumulatedMs(120_000), 30_000);
});

test("裁剪：突变点滑出窗口后被压缩，内存有界且结果不变", () => {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  integrator.addSample(0, 20);
  integrator.addSample(10_000, 85);
  integrator.addSample(40_000, 75);
  assert.equal(integrator.retainedTransitions, 2);
  // 推进到 t=200s，所有突变点均已滑出窗口
  integrator.addSample(200_000, 30);
  assert.equal(integrator.retainedTransitions, 0);
  assert.equal(integrator.accumulatedMs(200_000), 0);
});

test("边界：无采样时累计为 0", () => {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  assert.equal(integrator.accumulatedMs(123_456), 0);
});

test("边界：查询窗口早于流起点时只从首个采样点计起", () => {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  integrator.addSample(10_000, 85); // 首个采样即告警
  // t=20s：窗口理论起点为 -80s，但流从 t=10s 才开始，只计 10s
  assert.equal(integrator.accumulatedMs(20_000), 10_000);
});

test("非法输入：乱序采样与非法窗口被拒绝", () => {
  const integrator = new SlidingWindowConditionIntegrator(WINDOW_MS, OVER_TEMP);
  integrator.addSample(1_000, 85);
  assert.throws(() => integrator.addSample(999, 20), RangeError);
  assert.throws(
    () => new SlidingWindowConditionIntegrator(0, OVER_TEMP),
    RangeError,
  );
  assert.throws(
    () => new SlidingWindowConditionIntegrator(Number.NaN, OVER_TEMP),
    RangeError,
  );
});
