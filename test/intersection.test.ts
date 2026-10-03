import { test } from "node:test";
import assert from "node:assert/strict";
import { computeContactIntervals, positionAt } from "../src/intersection.ts";
import type { TrajectoryPoint } from "../src/types.ts";

function pt(t: number, x: number, y: number = 0, z: number = 0): TrajectoryPoint {
  return { t, x, y, z };
}

test("静止两实体持续接触：整个重叠时段为一个区间", () => {
  const a = [pt(0, 0), pt(100, 0)];
  const b = [pt(0, 5), pt(100, 5)];
  const intervals = computeContactIntervals(a, b, 10);
  assert.equal(intervals.length, 1);
  assert.equal(intervals[0].tStart, 0);
  assert.equal(intervals[0].tEnd, 100);
  assert.ok(Math.abs(intervals[0].minDistance - 5) < 1e-9);
});

test("匀速擦肩而过：区间端点与解析解一致", () => {
  // A 沿 x 轴以 1 m/s 从 (-100,0) 到 (100,0)，B 静止于原点，阈值 10m。
  const a = [pt(0, -100), pt(200, 100)];
  const b = [pt(0, 0), pt(200, 0)];
  const intervals = computeContactIntervals(a, b, 10);
  assert.equal(intervals.length, 1);
  assert.ok(Math.abs(intervals[0].tStart - 90) < 1e-9);
  assert.ok(Math.abs(intervals[0].tEnd - 110) < 1e-9);
  assert.ok(intervals[0].minDistance < 1e-9);
  assert.ok(Math.abs(intervals[0].tClosest - 100) < 1e-9);
});

test("距离始终大于阈值：无相遇", () => {
  const a = [pt(0, 0), pt(100, 0)];
  const b = [pt(0, 50), pt(100, 50)];
  assert.equal(computeContactIntervals(a, b, 10).length, 0);
});

test("轨迹时间不重叠：无相遇", () => {
  const a = [pt(0, 0), pt(10, 0)];
  const b = [pt(20, 0), pt(30, 0)];
  assert.equal(computeContactIntervals(a, b, 100).length, 0);
});

test("两次接近产生两个区间", () => {
  // A 以 2m/s 往返经过原点：x=-100 -> 100 -> -100，B 静止原点，阈值 15m。
  const a = [pt(0, -100), pt(100, 100), pt(200, -100)];
  const b = [pt(0, 0), pt(200, 0)];
  const intervals = computeContactIntervals(a, b, 15);
  assert.equal(intervals.length, 2);
  assert.ok(Math.abs(intervals[0].tStart - 42.5) < 1e-9);
  assert.ok(Math.abs(intervals[0].tEnd - 57.5) < 1e-9);
  assert.ok(Math.abs(intervals[1].tStart - 142.5) < 1e-9);
  assert.ok(Math.abs(intervals[1].tEnd - 157.5) < 1e-9);
});

test("接触时段跨越轨迹折点时被合并为单一区间", () => {
  // A 的折点 (t=100, x=5) 就在阈值球内，接触从窗口 1 延续到窗口 2。
  const a = [pt(0, -100), pt(100, 5), pt(200, -100)];
  const b = [pt(0, 0), pt(200, 0)];
  const intervals = computeContactIntervals(a, b, 15);
  assert.equal(intervals.length, 1, "穿越折点的接触应合并为单一区间");
  assert.ok(Math.abs(intervals[0].tStart - 85 / 1.05) < 1e-9);
  assert.ok(Math.abs(intervals[0].tEnd - (100 + 20 / 1.05)) < 1e-9);
  // A 在 t≈95.24 处穿过原点，最近距离为 0。
  assert.ok(intervals[0].minDistance < 1e-9);
  assert.ok(Math.abs(intervals[0].tClosest - 100 / 1.05) < 1e-9);
});

test("三维距离：z 轴差参与阈值判定", () => {
  const a = [pt(0, 0, 0, 0), pt(10, 0, 0, 0)];
  const b = [pt(0, 0, 0, 8), pt(10, 0, 0, 8)];
  assert.equal(computeContactIntervals(a, b, 7).length, 0);
  assert.equal(computeContactIntervals(a, b, 9).length, 1);
});

test("positionAt 线性插值与端点截断", () => {
  const traj = [pt(0, 0), pt(10, 20), pt(20, 0)];
  assert.deepEqual(positionAt(traj, 5), { x: 10, y: 0, z: 0 });
  assert.deepEqual(positionAt(traj, 15), { x: 10, y: 0, z: 0 });
  assert.deepEqual(positionAt(traj, -5), { x: 0, y: 0, z: 0 });
  assert.deepEqual(positionAt(traj, 25), { x: 0, y: 0, z: 0 });
});
