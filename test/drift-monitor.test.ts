import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock, AdjustableClock } from "../src/clock.ts";
import { ClockDriftMonitor } from "../src/drift-monitor.ts";
import type { DriftSample } from "../src/drift-monitor.ts";

test("正常走时：观测阶跃为 0，判定安全", () => {
  const monotonic = new ManualClock(0);
  const wall = new AdjustableClock(monotonic);
  const monitor = new ClockDriftMonitor(wall, monotonic, 500);

  monotonic.advance(3_000);
  const sample = monitor.sample();
  assert.equal(sample.observedStepMs, 0);
  assert.equal(sample.safe, true);
});

test("前拨 1 秒超出 500ms 容忍：判定不安全并通知监听者", () => {
  const monotonic = new ManualClock(0);
  const wall = new AdjustableClock(monotonic);
  const monitor = new ClockDriftMonitor(wall, monotonic, 500);

  const events: DriftSample[] = [];
  monitor.onUnsafe((sample) => events.push(sample));

  monotonic.advance(1_000);
  wall.adjust(1_000);
  const sample = monitor.sample();

  assert.equal(sample.observedStepMs, 1_000);
  assert.equal(sample.safe, false);
  assert.equal(events.length, 1);
  assert.equal(events[0].toleranceMs, 500);
});

test("容忍边界：恰好 500ms 的阶跃判定安全，501ms 判定不安全", () => {
  const monoA = new ManualClock(0);
  const wallA = new AdjustableClock(monoA);
  const monitorA = new ClockDriftMonitor(wallA, monoA, 500);
  wallA.adjust(500);
  assert.equal(monitorA.sample().safe, true);

  const monoB = new ManualClock(0);
  const wallB = new AdjustableClock(monoB);
  const monitorB = new ClockDriftMonitor(wallB, monoB, 500);
  wallB.adjust(501);
  assert.equal(monitorB.sample().safe, false);
});

test("后拨突变同样被检测（绝对值判定）", () => {
  const monotonic = new ManualClock(0);
  const wall = new AdjustableClock(monotonic);
  const monitor = new ClockDriftMonitor(wall, monotonic, 500);

  monotonic.advance(2_000);
  wall.adjust(-1_000);
  const sample = monitor.sample();
  assert.equal(sample.observedStepMs, -1_000);
  assert.equal(sample.safe, false);
});
