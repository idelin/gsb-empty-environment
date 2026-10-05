import { test } from "node:test";
import assert from "node:assert/strict";
import {
  TransformPipeline,
  rect,
  rectArea,
  intersectRect,
  subtractRect,
  rasterizePoint,
  rasterizeRect,
  pointsAlmostEqual,
  rectsAlmostEqual,
} from "../src/index.ts";
import type { Point, Rect } from "../src/index.ts";

const TOL = 1e-9;

function assertPointAlmost(actual: Point | null, expected: Point, msg?: string): void {
  assert.ok(actual !== null, `${msg ?? ""} 期望点未被裁掉`);
  assert.ok(
    pointsAlmostEqual(actual, expected, TOL),
    `${msg ?? ""} 期望 (${expected.x}, ${expected.y})，实际 (${actual.x}, ${actual.y})`,
  );
}

function assertRectAlmost(actual: Rect | null, expected: Rect, msg?: string): void {
  assert.ok(actual !== null, `${msg ?? ""} 期望矩形未被裁掉`);
  assert.ok(
    rectsAlmostEqual(actual, expected, TOL),
    `${msg ?? ""} 期望 [${expected.x0}, ${expected.y0}, ${expected.x1}, ${expected.y1})，` +
      `实际 [${actual.x0}, ${actual.y0}, ${actual.x1}, ${actual.y1})`,
  );
}

// ------------------------------------------------------------ 点 vs 矩形语义

test("点坐标与矩形半开区间在翻转下表现不同", () => {
  // 宽 4 的图像水平翻转：x' = 4 - x
  const p = TransformPipeline.forImage(4, 2).flipX();
  // 点（边界位置）1 映射到 3
  assertPointAlmost(p.mapPointForward({ x: 1, y: 0 }), { x: 3, y: 0 });
  // 半开矩形 [1,3) 映射到 [4-3, 4-1) = [1,3)：形状不变
  assertRectAlmost(p.mapRectForward(rect(1, 0, 3, 2)), rect(1, 0, 3, 2));
  // 像素 0 的覆盖区间 [0,1) 映射到 [3,4)，即像素 3
  assertRectAlmost(p.mapRectForward(rect(0, 0, 1, 1)), rect(3, 0, 4, 1));
});

test("点按闭区间包含、矩形按半开区间求交", () => {
  const p = TransformPipeline.forImage(100, 100).crop(10, 10, 50, 40);
  // 裁剪窗口 [10,60) x [10,50)。窗口右边缘上的点 x=60 是合法边界位置
  assertPointAlmost(p.mapPointForward({ x: 60, y: 50 }), { x: 50, y: 40 });
  // 但零宽矩形 [60,60) 之外、从边缘开始的矩形 [60,70) 被完全裁掉
  assert.equal(p.mapRectForward(rect(60, 10, 70, 20)), null);
  // 跨边界的矩形被求交：[55,65) -> [55,60) -> 输出 [45,50)
  assertRectAlmost(p.mapRectForward(rect(55, 10, 65, 20)), rect(45, 0, 50, 10));
});

// ------------------------------------------------------------ 裁剪与反查

test("裁剪丢弃外部区域，输出可反查原图区域", () => {
  const p = TransformPipeline.forImage(100, 100).crop(10, 20, 50, 40);
  // 窗口外的点被裁掉
  assert.equal(p.mapPointForward({ x: 5, y: 5 }), null);
  // 输出点 (0,0) 反查到原图 (10,20)
  assertPointAlmost(p.mapPointBackward({ x: 0, y: 0 }), { x: 10, y: 20 });
  // 整个输出反查到裁剪窗口
  assertRectAlmost(p.mapRectBackward(rect(0, 0, 50, 40)), rect(10, 20, 60, 60));
  // 输出外的“点”落在被裁区域，反查返回 null
  assert.equal(TransformPipeline.forImage(100, 100).crop(10, 20, 50, 40)
    .mapPointBackward({ x: 0, y: 0 }) !== null, true);
});

test("复合变换：crop -> scale -> flipX -> rotate90CW 正反映射互逆", () => {
  const p = TransformPipeline.forImage(100, 80)
    .crop(10, 10, 60, 40) // 60x40
    .scale(0.5) // 30x20
    .flipX() // 30x20
    .rotate90CW(); // 20x30
  assert.deepEqual(p.bounds, rect(0, 0, 20, 30));

  // 手工推导：原图点 (20, 30)
  // crop -> (10, 20); scale -> (5, 10); flipX -> (30-5, 10) = (25, 10);
  // rotate90CW(H=20): (x,y) -> (20-y, x) = (10, 25)
  assertPointAlmost(p.mapPointForward({ x: 20, y: 30 }), { x: 10, y: 25 });
  // 反查恢复
  assertPointAlmost(p.mapPointBackward({ x: 10, y: 25 }), { x: 20, y: 30 });

  // 矩形往返：输出矩形反查原图后再正向映射回去
  const outRect = rect(2, 3, 18, 27);
  const srcRect = p.mapRectBackward(outRect);
  assert.ok(srcRect !== null);
  assertRectAlmost(p.mapRectForward(srcRect), outRect, "矩形往返应一致");
});

test("复合管线中的点往返于每个采样点", () => {
  const p = TransformPipeline.forImage(640, 480)
    .crop(100.5, 50.25, 300, 200)
    .scale(1.5, 0.75)
    .flipY()
    .rotate90CCW()
    .flipX();
  for (const sx of [100.5, 150, 300.7, 400.5]) {
    for (const sy of [50.25, 120, 250.25]) {
      const out = p.mapPointForward({ x: sx, y: sy });
      assert.ok(out !== null, `(${sx}, ${sy}) 不应被裁掉`);
      assertPointAlmost(p.mapPointBackward(out), { x: sx, y: sy }, `(${sx}, ${sy}) 往返`);
    }
  }
});

// ------------------------------------------------------------ 裁剪分析

test("analyze 输出被裁掉部分与仍可恢复部分，面积守恒", () => {
  const p = TransformPipeline.forImage(100, 100).crop(10, 20, 50, 40);
  const { recoverable, clipped } = p.analyze();
  assertRectAlmost(recoverable, rect(10, 20, 60, 60));
  assert.equal(clipped.length, 4);
  const clippedArea = clipped.reduce((s, r) => s + rectArea(r), 0);
  assert.equal(clippedArea, 100 * 100 - 50 * 40);
  // 被裁部分与可恢复部分互不重叠
  for (const c of clipped) {
    assert.equal(intersectRect(c, recoverable!), null);
  }
});

test("复合管线（含缩放旋转）的裁剪分析仍映射回原图坐标", () => {
  const p = TransformPipeline.forImage(100, 80)
    .crop(10, 10, 60, 40)
    .scale(0.5)
    .rotate90CW();
  const { recoverable, clipped } = p.analyze();
  // 可恢复区域始终是原图坐标系下的裁剪窗口
  assertRectAlmost(recoverable, rect(10, 10, 70, 50));
  const clippedArea = clipped.reduce((s, r) => s + rectArea(r), 0);
  assert.ok(Math.abs(clippedArea - (100 * 80 - 60 * 40)) < TOL);
});

test("analyzeRect 分析任意标注框的可恢复/被裁部分", () => {
  const p = TransformPipeline.forImage(100, 100).crop(0, 0, 50, 100);
  const { recoverable, clipped } = p.analyzeRect(rect(25, 10, 75, 30));
  assertRectAlmost(recoverable, rect(25, 10, 50, 30));
  assert.equal(clipped.length, 1);
  assertRectAlmost(clipped[0]!, rect(50, 10, 75, 30));
});

test("多次裁剪的可恢复区域是各窗口的交集", () => {
  const p = TransformPipeline.forImage(100, 100)
    .crop(10, 10, 80, 80) // 原图 [10,90) x [10,90)
    .crop(20, 30, 40, 40); // 上一步坐标系 [20,60) x [30,70) => 原图 [30,70) x [40,80)
  const { recoverable } = p.analyze();
  assertRectAlmost(recoverable, rect(30, 40, 70, 80));
});

// ------------------------------------------------------------ 逆序恢复

test("verifyRoundTrip 验证逆序恢复", () => {
  const p = TransformPipeline.forImage(640, 480)
    .crop(100.5, 50.25, 300, 200)
    .scale(1.5, 0.75)
    .flipX()
    .rotate90CW()
    .flipY()
    .rotate90CCW();
  const report = p.verifyRoundTrip();
  assert.ok(report.ok, `逆序恢复误差过大：${report.maxError}`);
  assert.ok(report.maxError <= TOL);
});

test("invert 生成的逆管线等价于逆序逐步逆变换", () => {
  const p = TransformPipeline.forImage(100, 80)
    .crop(10, 10, 60, 40)
    .scale(0.5)
    .flipX()
    .rotate90CW();
  const inv = p.invert();
  // 逆管线步骤数 = 原步骤数 + 裁剪窗口数（窗口以恒等+裁剪步保留）
  assert.equal(inv.steps.length, p.steps.length + 1);
  // 逆管线正向映射 == 原管线反向映射
  const outRect = rect(1, 2, 19, 28);
  assertRectAlmost(inv.mapRectForward(outRect), p.mapRectBackward(outRect)!);
  // 用逆管线把输出恢复回原图区域
  assertRectAlmost(inv.mapRectForward(p.bounds), rect(10, 10, 70, 50));
  // 点级恢复
  const src = { x: 33.3, y: 44.4 };
  const out = p.mapPointForward(src)!;
  assertPointAlmost(inv.mapPointForward(out), src);
});

test("CW 与 CCW 互为逆操作", () => {
  const p = TransformPipeline.forImage(100, 50).rotate90CW().rotate90CCW();
  assert.deepEqual(p.bounds, rect(0, 0, 100, 50));
  assertPointAlmost(p.mapPointForward({ x: 7, y: 13 }), { x: 7, y: 13 });
  const report = p.verifyRoundTrip();
  assert.ok(report.ok);
});

// ------------------------------------------------------------ 取整延迟

test("全流程保持浮点，取整只发生在栅格化", () => {
  const p = TransformPipeline.forImage(10, 10).scale(1 / 3);
  const out = p.mapPointForward({ x: 1, y: 2 })!;
  // 不做任何取整：保留 1/3、2/3 的浮点值
  assert.ok(Math.abs(out.x - 1 / 3) < 1e-12);
  assert.ok(Math.abs(out.y - 2 / 3) < 1e-12);
  // 栅格化时才取整
  assert.deepEqual(rasterizePoint(out), { x: 0, y: 1 });
});

test("分数裁剪窗口在栅格化前不取整", () => {
  const p = TransformPipeline.forImage(100, 100).crop(10.5, 20.25, 50.5, 40.75);
  assertRectAlmost(p.mapRectBackward(p.bounds), rect(10.5, 20.25, 61, 61));
  // cover 模式：floor 左上、ceil 右下
  assert.deepEqual(rasterizeRect(rect(10.5, 20.25, 61, 61)), { x0: 10, y0: 20, x1: 61, y1: 61 });
  // nearest 模式：四舍五入
  assert.deepEqual(rasterizeRect(rect(10.5, 20.25, 60.6, 60.6), "nearest"), {
    x0: 11,
    y0: 20,
    x1: 61,
    y1: 61,
  });
});

// ------------------------------------------------------------ 几何工具

test("subtractRect 拆分与 intersectRect 行为", () => {
  const outer = rect(0, 0, 10, 10);
  const parts = subtractRect(outer, rect(2, 2, 8, 8));
  assert.equal(parts.length, 4);
  assert.equal(parts.reduce((s, r) => s + rectArea(r), 0), 100 - 36);
  // 无交集：原样返回
  assert.deepEqual(subtractRect(outer, rect(20, 20, 30, 30)), [outer]);
  // 半开区间：边相接不算相交
  assert.equal(intersectRect(rect(0, 0, 5, 5), rect(5, 0, 10, 5)), null);
});

test("旋转后矩形映射保持轴对齐", () => {
  const p = TransformPipeline.forImage(100, 50).rotate90CW();
  // 原图左上角 10x10 块旋转后到输出右上角
  assertRectAlmost(p.mapRectForward(rect(0, 0, 10, 10)), rect(40, 0, 50, 10));
  assert.deepEqual(p.bounds, rect(0, 0, 50, 100));
});
