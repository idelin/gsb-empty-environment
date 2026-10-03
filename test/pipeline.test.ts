/**
 * 流水线验收测试：
 *  - 10 个复杂算子的查询流水线正确性；
 *  - 稳态执行期堆分配计数为零；
 *  - 无 GC 停顿；
 *  - 内存曲线平直。
 */
import assert from "node:assert/strict";
import { test } from "node:test";
import { CHUNK_SIZE, ChunkPool } from "../src/chunk.ts";
import { MemoryMonitor } from "../src/monitor.ts";
import {
  AggSum,
  FilterGe,
  MapAbs,
  MapAdd,
  MapClamp,
  MapMul,
  MapNegate,
  MapSin,
  MapSqrt,
  ScanSource,
} from "../src/operators.ts";
import { Pipeline } from "../src/pipeline.ts";

const SEED = 0x9e3779b9;

function buildPipeline(): { pool: ChunkPool; pipeline: Pipeline } {
  const pool = new ChunkPool(16);
  const pipeline = new Pipeline(
    pool,
    new ScanSource(SEED),
    [
      new FilterGe(-900),
      new MapAdd(500),
      new MapMul(1.5),
      new MapSqrt(),
      new MapAbs(),
      new MapClamp(0, 60),
      new MapSin(),
      new MapNegate(),
    ],
    new AggSum(),
  );
  return { pool, pipeline };
}

/** 纯 JS 参考实现：与流水线逐算子语义一一对应（仅复现首轮数据）。 */
function referenceFirstRound(): number {
  let s = SEED >>> 0;
  let acc = 0;
  for (let i = 0; i < CHUNK_SIZE; i++) {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    let v = (s / 4294967296) * 2000 - 1000; // scan
    if (v < -900) continue; // filter
    v += 500; // map add
    v *= 1.5; // map mul
    v = Math.sqrt(Math.abs(v)); // sqrt
    v = Math.abs(v); // abs
    v = Math.min(60, Math.max(0, v)); // clamp
    v = Math.sin(v); // sin
    v = -v; // negate
    acc += v; // agg sum
  }
  return acc;
}

test("流水线包含 10 个算子", () => {
  const { pipeline } = buildPipeline();
  assert.equal(pipeline.operatorCount, 10);
});

test("流水线计算结果与参考实现一致", () => {
  const { pipeline } = buildPipeline();
  const got = pipeline.runOnceScalar();
  assert.equal(got, referenceFirstRound());
});

test("稳态执行期堆分配计数为零且借还平衡", () => {
  const { pool, pipeline } = buildPipeline();
  pipeline.run(2_000); // 预热
  const frozen = pool.stats().heapAllocations;

  pipeline.run(10_000);
  const s = pool.stats();
  assert.equal(s.heapAllocations, frozen, "稳态堆分配计数必须冻结");
  assert.equal(s.acquires, s.releases, "Chunk 借还必须平衡");
  assert.equal(s.inUseCount, 0, "执行完毕无 Chunk 泄漏");
});

test("稳态执行期无 GC 事件", async () => {
  const { pipeline } = buildPipeline();
  const monitor = new MemoryMonitor(64);
  monitor.start();

  pipeline.run(8_000); // 预热（JIT 稳定）
  await monitor.flush();
  const gcBaseline = monitor.gcEvents;

  pipeline.run(8_000); // 稳态验收区间
  await monitor.flush();
  monitor.stop();

  assert.equal(monitor.gcEvents - gcBaseline, 0, "稳态区间不应发生任何 GC");
});

test("稳态执行期内存曲线平直", () => {
  const { pipeline } = buildPipeline();
  const monitor = new MemoryMonitor(64);

  pipeline.run(8_000); // 预热
  monitor.sample();

  const checksum = new Float64Array(1);
  for (let i = 1; i <= 10_000; i++) {
    pipeline.runOnce();
    checksum[0] += pipeline.result[0];
    if (i % 1_000 === 0) monitor.sample();
  }

  const amplitude = monitor.heapAmplitude(1);
  assert.ok(
    amplitude < 256 * 1024,
    `heapUsed 振幅应平直（实际 ${(amplitude / 1024).toFixed(1)} KiB）`,
  );
});

test("同一批 Chunk 槽位在算子间循环复用", () => {
  const { pool, pipeline } = buildPipeline();
  pipeline.run(100);
  const s1 = pool.stats();
  pipeline.run(100);
  const s2 = pool.stats();
  // 每轮：scan 借 1 次 + 8 个转换算子各借 1 次 = 9 次 acquire，全部归还
  assert.equal(s2.acquires - s1.acquires, 9 * 100);
  assert.equal(s2.releases - s1.releases, 9 * 100);
  assert.equal(s2.heapAllocations, s1.heapAllocations);
});
