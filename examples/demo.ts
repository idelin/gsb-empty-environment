import { TransformPipeline, rasterizeRect, rect } from "../src/index.ts";

// 场景：640x480 原图 -> 裁剪 -> 缩放 -> 水平翻转 -> 顺时针 90 度
const pipeline = TransformPipeline.forImage(640, 480)
  .crop(100.5, 50.25, 300, 200)
  .scale(1.5, 0.75)
  .flipX()
  .rotate90CW();

console.log("输出尺寸:", pipeline.bounds);

// 1) 反查原图区域：输出中的一个检测框对应原图哪里？
const detectionOut = rect(10, 20, 100, 120);
const srcRegion = pipeline.mapRectBackward(detectionOut);
console.log("输出框", detectionOut, "对应原图区域:", srcRegion);
console.log("栅格化（最终才取整）:", srcRegion && rasterizeRect(srcRegion));

// 2) 被裁掉部分 vs 仍可恢复部分
const { recoverable, clipped } = pipeline.analyze();
console.log("仍可恢复:", recoverable);
console.log("被裁掉:", clipped);

// 3) 逆序恢复验证
console.log("逆序恢复验证:", pipeline.verifyRoundTrip());
