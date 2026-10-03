import { test } from "node:test";
import assert from "node:assert/strict";
import { SpacetimeGridIndex } from "../src/grid_index.ts";
import type { TrajectoryPoint } from "../src/types.ts";

function pt(t: number, x: number, y: number = 0): TrajectoryPoint {
  return { t, x, y, z: 0 };
}

test("倒排网格：同切片同网格的实体被预筛命中", () => {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 1000, cellSizeMeters: 10 });
  index.insert("A", [pt(0, 0), pt(500, 5)]);
  index.insert("B", [pt(100, 3), pt(600, 4)]);
  const slices = index.candidateSlices("A", "B", 10);
  assert.deepEqual(slices, [0]);
});

test("空间相距过远的实体不会被预筛命中", () => {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 1000, cellSizeMeters: 10 });
  index.insert("A", [pt(0, 0), pt(500, 0)]);
  index.insert("B", [pt(0, 500), pt(500, 500)]);
  assert.deepEqual(index.candidateSlices("A", "B", 10), []);
});

test("跨切片轨迹段在边界插值，目标切片自包含", () => {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 1000, cellSizeMeters: 10 });
  // 段从 t=500 到 t=2500，跨越切片 0、1、2。
  index.insert("A", [pt(500, 0), pt(2500, 200)]);
  index.insert("B", [pt(1200, 80), pt(1800, 120)]);
  const slices = index.candidateSlices("A", "B", 20);
  assert.deepEqual(slices, [1], "只有切片 1 内两实体网格邻域相交");
  const ptsA = index.pointsInSlices("A", [1]);
  assert.equal(ptsA[0].t, 1000, "切片下界处应有插值边界点");
  assert.equal(ptsA[ptsA.length - 1].t, 2000, "切片上界处应有插值边界点");
  assert.ok(Math.abs(ptsA[0].x - 50) < 1e-9);
});

test("pointsInSlices 输出按时间排序且去重", () => {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 1000, cellSizeMeters: 10 });
  index.insert("A", [pt(900, 0), pt(1100, 20), pt(2100, 40)]);
  const pts = index.pointsInSlices("A", [1, 0, 2]);
  const times = pts.map((p) => p.t);
  assert.deepEqual(times, [...times].sort((a, b) => a - b));
  assert.equal(new Set(times).size, times.length);
});

test("单点轨迹与空轨迹的边界情形", () => {
  const index = new SpacetimeGridIndex({ sliceDurationMs: 1000, cellSizeMeters: 10 });
  index.insert("A", [pt(100, 0)]);
  index.insert("B", []);
  assert.deepEqual(index.candidateSlices("A", "B", 10), []);
  assert.equal(index.trajectoryOf("A").length, 1);
});
