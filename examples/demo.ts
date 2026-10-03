/**
 * demo.ts —— 运行样例。
 *
 * 构建一条含 10 个算子的向量化查询流水线，连续执行 20000 轮，
 * 期间用内存监控器采样堆曲线、统计 GC 事件与堆分配计数，
 * 验证"流水线建立后零堆分配、无 GC 停顿、内存曲线平直"。
 *
 * 运行：npm run demo
 */

import { ChunkPool } from "../src/chunk.ts";
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

const WARMUP = 10_000;
const SETTLE = 2_000;
const ITERATIONS = 20_000;
const SAMPLE_EVERY = 2_000;

// ---------- 建立期（允许分配） ----------
const pool = new ChunkPool(16);
const pipeline = new Pipeline(
  pool,
  new ScanSource(0x9e3779b9),
  [
    new FilterGe(-900), // op#2  选择过滤（向量压实）
    new MapAdd(500), //    op#3  平移
    new MapMul(1.5), //    op#4  缩放
    new MapSqrt(), //      op#5  平方根
    new MapAbs(), //       op#6  绝对值
    new MapClamp(0, 60), //op#7  区间截断
    new MapSin(), //       op#8  三角函数
    new MapNegate(), //    op#9  取负
  ],
  new AggSum(), //         op#10 聚合求和
);

const monitor = new MemoryMonitor(64);
monitor.start();

/** 校验和累加单元：写入 Float64Array 不产生任何装箱。 */
const checksum = new Float64Array(1);

/**
 * 稳态执行循环：累加经由预分配单元进行，
 * 整个循环体内不创建任何对象 —— 这是零堆分配的关键约束。
 */
function runSteadyState(iterations: number): void {
  for (let i = 1; i <= iterations; i++) {
    pipeline.runOnce();
    checksum[0] += pipeline.result[0];
    if (i % SAMPLE_EVERY === 0) monitor.sample();
  }
}

async function main(): Promise<void> {
  console.log("=== 向量化中间结果内存池 · 运行样例 ===");
  console.log(`流水线算子数: ${pipeline.operatorCount}`);
  console.log(`Chunk 池容量: ${pool.stats().capacity} 块 × 1024 槽位 (Float64)`);
  console.log(`建立期堆分配次数: ${pool.stats().heapAllocations}（仅预分配阶段）`);
  console.log("");

  // ---------- 预热（JIT 编译稳定，属于流水线建立的一部分） ----------
  pipeline.run(WARMUP);
  await monitor.flush();
  // 静置消化期：让运行时完成优化代码提交等收尾工作
  pipeline.run(SETTLE);
  await monitor.flush();
  const gcBaseline = monitor.gcEvents;
  const pauseBaseline = monitor.gcTotalPauseMs;
  const allocsAfterWarmup = pool.stats().heapAllocations;
  monitor.sample();

  // ---------- 稳态执行（验收区间） ----------
  runSteadyState(ITERATIONS);
  await monitor.flush(); // 等待 GC 通知异步投递
  monitor.stop();

  const stats = pool.stats();
  const gcDuringSteadyState = monitor.gcEvents - gcBaseline;
  const pauseDuringSteadyState = monitor.gcTotalPauseMs - pauseBaseline;
  const amplitude = monitor.heapAmplitude(1);

  console.log("--- 稳态执行结果（验收区间） ---");
  console.log(`执行轮数:            ${ITERATIONS}`);
  console.log(`校验和 (checksum):   ${checksum[0].toFixed(6)}`);
  console.log(`堆分配增量:          ${stats.heapAllocations - allocsAfterWarmup} 次`);
  console.log(`Chunk 借还次数:      acquire=${stats.acquires} release=${stats.releases}`);
  console.log(`GC 事件数:           ${gcDuringSteadyState}`);
  console.log(`GC 停顿总时长:       ${pauseDuringSteadyState.toFixed(3)} ms`);
  console.log(`heapUsed 最大振幅:   ${(amplitude / 1024).toFixed(1)} KiB`);
  console.log("");

  console.log("--- 内存曲线（每 2000 轮采样） ---");
  console.log("  轮次   heapUsed(MiB)  heapTotal(MiB)   rss(MiB)");
  for (let i = 0; i < monitor.length; i++) {
    console.log(
      `  ${String(i * SAMPLE_EVERY).padStart(5)}   ` +
        `${(monitor.heapUsedAt(i) / 1048576).toFixed(2).padStart(12)}  ` +
        `${(monitor.heapTotalAt(i) / 1048576).toFixed(2).padStart(14)}  ` +
        `${(monitor.rssAt(i) / 1048576).toFixed(2).padStart(9)}`,
    );
  }

  const zeroAlloc = stats.heapAllocations === allocsAfterWarmup;
  const noGc = gcDuringSteadyState === 0;
  const flat = amplitude < 64 * 1024;
  console.log("");
  console.log(
    `验收: 零堆分配=${zeroAlloc ? "✔" : "✘"}  无GC停顿=${noGc ? "✔" : "✘"}  ` +
      `内存曲线平直(振幅<64KiB)=${flat ? "✔" : "✘"}`,
  );
  process.exit(zeroAlloc && noGc && flat ? 0 : 1);
}

await main();
