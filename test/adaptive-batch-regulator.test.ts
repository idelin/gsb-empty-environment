import { test } from "node:test";
import assert from "node:assert/strict";

import { CacheHierarchy } from "../src/cache.ts";
import { AdaptiveBatchSizeRegulator } from "../src/regulator.ts";
import { VectorizedEngine, WideTable } from "../src/engine.ts";

const L1 = 32 * 1024;

function makeEngine() {
  const cache = new CacheHierarchy();
  const regulator = new AdaptiveBatchSizeRegulator({
    l1CapacityBytes: cache.l1CapacityBytes,
  });
  return { cache, regulator, engine: new VectorizedEngine(cache, regulator) };
}

/** 内存带宽受限环境：L2 仅 64KB，1024 行宽行批（128KB）超出 L2，颠簸直达内存。 */
function makeBandwidthBoundEngine() {
  const cache = new CacheHierarchy(32 * 1024, 64 * 1024);
  const regulator = new AdaptiveBatchSizeRegulator({
    l1CapacityBytes: cache.l1CapacityBytes,
  });
  return { cache, regulator, engine: new VectorizedEngine(cache, regulator) };
}

test("静态目标：行宽 128B 时目标批大小为 256（256*128B = 32KB = L1）", () => {
  const regulator = new AdaptiveBatchSizeRegulator();
  regulator.setRowSize(128);
  assert.equal(regulator.targetBatchSize(), 256);
});

test("静态目标：行极窄（8B）时目标批大小放大至 4096", () => {
  const regulator = new AdaptiveBatchSizeRegulator();
  regulator.setRowSize(8);
  assert.equal(regulator.targetBatchSize(), 4096);
});

test("静态目标：夹在 [256, 4096] 边界内", () => {
  const regulator = new AdaptiveBatchSizeRegulator();
  regulator.setRowSize(1);
  assert.equal(regulator.targetBatchSize(), 4096);
  regulator.setRowSize(1024 * 1024);
  assert.equal(regulator.targetBatchSize(), 256);
});

test("宽行场景：批大小从 1024 动态收缩到 256，单批刚好容纳进 L1", () => {
  const { engine, regulator } = makeEngine();
  // 超宽宽表：16 列 * 8B = 128B/行
  const table = new WideTable({ rowCount: 8192, columnCount: 16 });
  const result = engine.runQuery(table, { passes: 4 });

  assert.equal(result.finalBatchSize, 256);
  // 轨迹应体现 1024 -> 512 -> 256 的动态下调
  const sizes = result.trace.map((t) => t.batchSize);
  assert.equal(sizes[0], 1024);
  assert.ok(sizes.includes(512), `轨迹中应出现 512: ${sizes}`);
  assert.ok(sizes.includes(256), `轨迹中应出现 256: ${sizes}`);
  // 收敛后单批足迹刚好 <= L1
  assert.ok(regulator.footprintBytes() <= L1);
  assert.equal(regulator.footprintBytes(), L1);
});

test("验收边界：超宽宽表查询，调节器感知内存带宽受限后避免缓存颠簸", () => {
  const { engine } = makeBandwidthBoundEngine();
  const table = new WideTable({ rowCount: 16384, columnCount: 16 });
  const result = engine.runQuery(table, { passes: 4 });

  const first = result.trace[0];
  const last = result.trace[result.trace.length - 1];

  // 初始 1024 行 * 128B = 128KB > L1(32KB)：颠簸，L1 命中率极低
  assert.equal(first.batchSize, 1024);
  assert.equal(first.fitsInL1, false);
  assert.ok(
    first.l1HitRate < 0.1,
    `初始批应发生颠簸，L1 命中率=${first.l1HitRate}`,
  );
  assert.ok(first.memoryReads > 0, "颠簸时应频繁回读内存（带宽受限）");

  // 收敛到 256 行 * 128B = 32KB：刚好容纳进 L1，多趟扫描命中
  assert.equal(last.batchSize, 256);
  assert.equal(last.fitsInL1, true);
  assert.ok(
    last.l1HitRate >= 0.7,
    `收敛后应保持缓存热态，L1 命中率=${last.l1HitRate}`,
  );
  // 收敛后仅剩首次加载的必然内存读（compulsory miss），不再有颠簸造成的重复回读
  assert.equal(last.memoryReads, last.footprintBytes / 64);
});

test("自适应相比固定 1024 显著降低停顿周期", () => {
  const adaptive = makeBandwidthBoundEngine();
  const fixed = makeBandwidthBoundEngine();
  const tableA = new WideTable({ rowCount: 16384, columnCount: 16 });
  const tableB = new WideTable({ rowCount: 16384, columnCount: 16 });

  const a = adaptive.engine.runQuery(tableA, { passes: 4 });
  const f = fixed.engine.runQuery(tableB, { passes: 4, fixedBatchSize: true });

  assert.equal(f.finalBatchSize, 1024);
  assert.equal(a.finalBatchSize, 256);
  assert.ok(
    a.estimatedCycles < f.estimatedCycles * 0.5,
    `自适应(${a.estimatedCycles}) 应远小于固定批(${f.estimatedCycles})`,
  );
  // 两种执行的结果必须一致
  assert.equal(a.checksum, f.checksum);
});

test("极窄行场景：批大小从 1024 放大到 4096", () => {
  const { engine } = makeEngine();
  const table = new WideTable({ rowCount: 65536, columnCount: 1 });
  const result = engine.runQuery(table, { passes: 4 });

  assert.equal(result.finalBatchSize, 4096);
  const sizes = result.trace.map((t) => t.batchSize);
  assert.equal(sizes[0], 1024);
  assert.ok(sizes.includes(2048));
  assert.ok(sizes.includes(4096));
});

test("批大小始终保持在 [256, 4096] 边界内", () => {
  const { engine, regulator } = makeEngine();
  for (const cols of [1, 4, 16, 64, 256]) {
    const table = new WideTable({ rowCount: 4096, columnCount: cols });
    engine.runQuery(table, { passes: 2 });
    for (const d of regulator.decisions) {
      assert.ok(d.batchSize >= 256 && d.batchSize <= 4096);
    }
  }
});

test("调节器决策序列稳定收敛、无振荡", () => {
  const { engine, regulator } = makeEngine();
  const table = new WideTable({ rowCount: 16384, columnCount: 16 });
  engine.runQuery(table, { passes: 4 });

  const tail = regulator.decisions.slice(-8);
  for (const d of tail) {
    assert.equal(d.action, "hold", "收敛后不应再调整");
    assert.equal(d.batchSize, 256);
  }
});

test("查询结果正确性：批大小变化不影响聚合结果", () => {
  const { engine } = makeEngine();
  const table = new WideTable({ rowCount: 10000, columnCount: 16 });
  const result = engine.runQuery(table, { passes: 4 });

  let expected = 0;
  for (let r = 0; r < table.rowCount; r++) expected += table.rowSum(r);
  assert.equal(result.checksum, expected);
  // 所有行恰好被处理一次
  const processed = result.trace.reduce((n, t) => n + t.rows, 0);
  assert.equal(processed, table.rowCount);
});
