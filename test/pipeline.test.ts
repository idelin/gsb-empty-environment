import { test } from "node:test";
import assert from "node:assert/strict";
import {
  TransformPipeline,
  applyPoint,
  applyRect,
  compose,
  IDENTITY,
  intersectRect,
  isEmptyRect,
  makeRect,
  pixelAt,
  rasterizeRect,
  rectArea,
  subtractRect,
  type Rect,
} from "../src/index.ts";

const EPS = 1e-9;

function assertRectAlmostEqual(actual: Rect, expected: Rect, eps = EPS) {
  assert.ok(
    Math.abs(actual.x0 - expected.x0) <= eps &&
      Math.abs(actual.y0 - expected.y0) <= eps &&
      Math.abs(actual.x1 - expected.x1) <= eps &&
      Math.abs(actual.y1 - expected.y1) <= eps,
    `expected ${JSON.stringify(actual)} ≈ ${JSON.stringify(expected)}`,
  );
}

function assertPointAlmostEqual(
  actual: { x: number; y: number },
  expected: { x: number; y: number },
  eps = EPS,
) {
  assert.ok(
    Math.abs(actual.x - expected.x) <= eps &&
      Math.abs(actual.y - expected.y) <= eps,
    `expected ${JSON.stringify(actual)} ≈ ${JSON.stringify(expected)}`,
  );
}

/** 构造一条包含全部四类操作的复合流水线。 */
function buildComposite(): TransformPipeline {
  return new TransformPipeline(640, 480)
    .crop(makeRect(10, 20, 630, 460))
    .scale(1.5, 0.75)
    .flipH()
    .rotate90(1)
    .flipV()
    .crop(makeRect(5, 15, 300, 900));
}

test("逆序恢复：复合变换后点坐标可精确还原", () => {
  const p = buildComposite();
  const samples = [
    { x: 0, y: 0 },
    { x: 639.5, y: 479.25 },
    { x: 100.125, y: 200.875 },
    { x: 320, y: 240 },
  ];
  for (const s of samples) {
    const mapped = p.mapPoint(s);
    assertPointAlmostEqual(p.unmapPoint(mapped), s);
  }
});

test("逆序恢复：逐步逆序回放与整体逆矩阵一致", () => {
  const p = buildComposite();
  const steps = p.steps;

  // 方式一：库内维护的复合逆矩阵
  const viaPipeline = p.unmapPoint({ x: 12.5, y: 34.5 });

  // 方式二：调用方把每步的逆矩阵按相反顺序自行回放
  let manual = { x: 12.5, y: 34.5 };
  for (let i = steps.length - 1; i >= 0; i--) {
    manual = applyPoint(steps[i].inverse, manual);
  }
  assertPointAlmostEqual(manual, viaPipeline);

  // 方式三：restoreMatrix()（逆序复合）与逐步回放一致
  const restored = p.restoreMatrix();
  assertPointAlmostEqual(applyPoint(restored, { x: 12.5, y: 34.5 }), manual);

  // 画布尺寸链：每步的 before/after 必须首尾衔接
  assert.equal(steps[0].canvasBefore.width, 640);
  assert.equal(steps[0].canvasBefore.height, 480);
  for (let i = 1; i < steps.length; i++) {
    assert.equal(steps[i].canvasBefore.width, steps[i - 1].canvasAfter.width);
    assert.equal(steps[i].canvasBefore.height, steps[i - 1].canvasAfter.height);
  }
  const last = steps[steps.length - 1];
  assert.equal(p.width, last.canvasAfter.width);
  assert.equal(p.height, last.canvasAfter.height);

  // 逆序恢复矩阵 ∘ 正向映射 = 恒等（点往返）
  assertPointAlmostEqual(
    applyPoint(p.restoreMatrix(), p.mapPoint({ x: 55, y: 77 })),
    { x: 55, y: 77 },
  );
});

test("逆序恢复：矩形区域可精确还原", () => {
  const p = buildComposite();
  const r = makeRect(50, 60, 200, 300);
  assertRectAlmostEqual(p.unmapRect(p.mapRect(r)), r);
});

test("裁剪：被裁掉部分与可恢复部分恰好拼回原图", () => {
  const p = new TransformPipeline(100, 80).crop(makeRect(10, 20, 70, 50));
  const recoverable = p.recoverableRegion();
  assertRectAlmostEqual(recoverable, makeRect(10, 20, 70, 50));

  const clipped = p.clippedRegions();
  assert.equal(clipped.length, 4); // 左、右、上、下四条
  const total =
    rectArea(recoverable) + clipped.reduce((sum, r) => sum + rectArea(r), 0);
  assert.equal(total, 100 * 80);

  // 各块互不重叠（半开区间求交为空）
  const all = [recoverable, ...clipped];
  for (let i = 0; i < all.length; i++) {
    for (let j = i + 1; j < all.length; j++) {
      assert.equal(intersectRect(all[i], all[j]), null);
    }
  }
});

test("裁剪：逐步记录丢失区域，非裁剪操作不丢失信息", () => {
  const p = new TransformPipeline(100, 100)
    .crop(makeRect(0, 0, 80, 100)) // 裁掉右侧 20px
    .flipH()
    .rotate90(1)
    .scale(0.5)
    .crop(makeRect(10, 0, 50, 40)); // 再裁一条

  const losses = p.steps.map((s) => s.lostInOriginal);
  assert.equal(losses[0].length, 1); // 第一次裁剪丢掉一块
  assertRectAlmostEqual(losses[0][0], makeRect(80, 0, 100, 100));
  assert.equal(losses[1].length, 0); // flip 无损
  assert.equal(losses[2].length, 0); // rotate 无损
  assert.equal(losses[3].length, 0); // scale 无损
  assert.equal(losses[4].length, 1); // 第二次裁剪丢掉一块

  // 所有丢失块 + 最终可恢复区域 = 原图（面积守恒且互不重叠）
  const lost = losses.flat();
  const total =
    rectArea(p.recoverableRegion()) +
    lost.reduce((sum, r) => sum + rectArea(r), 0);
  assert.ok(Math.abs(total - 100 * 100) < EPS);
  const all = [p.recoverableRegion(), ...lost];
  for (let i = 0; i < all.length; i++) {
    for (let j = i + 1; j < all.length; j++) {
      assert.equal(intersectRect(all[i], all[j]), null);
    }
  }
});

test("反查原图区域：结果坐标系中的矩形映射回原图", () => {
  const p = new TransformPipeline(400, 300)
    .crop(makeRect(100, 50, 300, 250)) // 原图区域 [100,300)×[50,250)
    .scale(0.5); // 画布变为 100×100
  assert.equal(p.width, 100);
  assert.equal(p.height, 100);

  // 结果画布中的 [0,50)×[0,50) 对应原图 [100,200)×[50,150)
  assertRectAlmostEqual(
    p.unmapRect(makeRect(0, 0, 50, 50)),
    makeRect(100, 50, 200, 150),
  );
  // 整个结果画布反查 = 可恢复区域
  assertRectAlmostEqual(
    p.unmapRect(makeRect(0, 0, p.width, p.height)),
    p.recoverableRegion(),
  );
});

test("点坐标与矩形半开区间的语义区分", () => {
  const p = new TransformPipeline(10, 4).flipH();
  // 点：x=3 映射到 10-3=7
  assert.deepEqual(p.mapPoint({ x: 3, y: 1 }), { x: 7, y: 1 });
  // 矩形（半开区间）：像素列 3 即 [3,4)，翻转后是 [6,7)，即像素列 6
  assertRectAlmostEqual(p.mapRect(makeRect(3, 0, 4, 4)), makeRect(6, 0, 7, 4));

  // 旋转 90°：点 (x,y)→(H-y,x)；矩形边界互换后仍是半开区间
  const q = new TransformPipeline(10, 4).rotate90(1);
  assert.deepEqual(q.mapPoint({ x: 3, y: 0 }), { x: 4, y: 3 });
  assertRectAlmostEqual(q.mapRect(makeRect(3, 0, 4, 4)), makeRect(0, 3, 4, 4));
  // 旋转后画布宽高互换
  assert.equal(q.width, 4);
  assert.equal(q.height, 10);
});

test("整数取整只发生在栅格化", () => {
  const p = new TransformPipeline(101, 51).scale(0.5);
  // 中间过程保持分数，绝不取整
  assert.equal(p.width, 50.5);
  assert.equal(p.height, 25.5);
  assert.deepEqual(p.mapPoint({ x: 3, y: 5 }), { x: 1.5, y: 2.5 });

  // 栅格化时才取整：覆盖 [0,50.5)×[0,25.5) 需要 51×26 个像素
  assert.deepEqual(rasterizeRect(makeRect(0, 0, p.width, p.height)), {
    x0: 0,
    y0: 0,
    x1: 51,
    y1: 26,
  });
  // 分数边界矩形 [0.5,3.5)×[1.5,2.5) 覆盖像素列 0..3、行 1..2
  assert.deepEqual(rasterizeRect(makeRect(0.5, 1.5, 3.5, 2.5)), {
    x0: 0,
    y0: 1,
    x1: 4,
    y1: 3,
  });
  // 点落入像素用 floor
  assert.deepEqual(pixelAt({ x: 1.5, y: 2.5 }), { x: 1, y: 2 });
});

test("半开区间：相邻瓦片无缝无重叠", () => {
  const a = makeRect(0, 0, 4, 4);
  const b = makeRect(4, 0, 8, 4);
  assert.equal(intersectRect(a, b), null); // 共享边界不算重叠
  assert.equal(rectArea(a) + rectArea(b), rectArea(makeRect(0, 0, 8, 4)));
  // 点 4 属于 b 不属于 a（由 pixelAt 的 floor 语义保证）
  assert.equal(pixelAt({ x: 4, y: 2 }).x, 4);
});

test("旋转与翻转的自逆性", () => {
  // 旋转 4 次回到原位
  const p = new TransformPipeline(30, 20)
    .rotate90(1)
    .rotate90(1)
    .rotate90(1)
    .rotate90(1);
  assert.equal(p.width, 30);
  assert.equal(p.height, 20);
  assert.deepEqual(p.mapPoint({ x: 7, y: 3 }), { x: 7, y: 3 });

  // 逆时针 = 顺时针 3 次
  const ccw = new TransformPipeline(30, 20).rotate90(-1);
  const cw3 = new TransformPipeline(30, 20).rotate90(3);
  assert.deepEqual(ccw.mapPoint({ x: 7, y: 3 }), cw3.mapPoint({ x: 7, y: 3 }));

  // 翻转两次 = 恒等
  const f = new TransformPipeline(30, 20).flipH().flipH().flipV().flipV();
  assert.deepEqual(f.mapPoint({ x: 7, y: 3 }), { x: 7, y: 3 });
  assertRectAlmostEqual(f.mapRect(makeRect(1, 2, 5, 9)), makeRect(1, 2, 5, 9));
});

test("subtractRect：inner 超出 outer 时先裁剪", () => {
  const parts = subtractRect(makeRect(0, 0, 10, 10), makeRect(5, 5, 20, 20));
  assert.equal(parts.length, 2); // 只剩左条和上条
  const total = parts.reduce((s, r) => s + rectArea(r), 0);
  assert.equal(total, 100 - 25);
});

test("异常输入", () => {
  assert.throws(() => new TransformPipeline(0, 10));
  assert.throws(() => new TransformPipeline(10, 10).scale(0));
  assert.throws(() =>
    new TransformPipeline(10, 10).crop(makeRect(20, 20, 30, 30)),
  );
  // 空矩形（半开区间退化）视为完全在外
  assert.ok(isEmptyRect(makeRect(5, 5, 5, 8)));
});

test("复合流水线端到端：标注框跟随图像变换并可反查", () => {
  // 原图 800×600，标注框 [100,120)×[80,200)
  const box = makeRect(100, 80, 120, 200);
  const p = new TransformPipeline(800, 600)
    .crop(makeRect(50, 50, 750, 550))
    .rotate90(1) // 顺时针 90°，画布 500×700
    .scale(0.25) // 缩到 125×175
    .flipH();

  const mapped = p.mapRect(box);
  // 全程分数精度：mapped 的边界不应被取整
  // 反查必须精确回到原标注框
  assertRectAlmostEqual(p.unmapRect(mapped), box);

  // 栅格化只在最后：把映射后的框落到输出像素网格
  const px = rasterizeRect(mapped);
  assert.ok(px.x1 > px.x0 && px.y1 > px.y0);
});
