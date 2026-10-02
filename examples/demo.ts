/**
 * 运行样例：自适应批大小调节器在超宽宽表 / 极窄表查询下的行为。
 * 运行：node examples/demo.ts   （需要 Node >= 22.6，原生 TypeScript 类型剥离）
 */

import { CacheHierarchy } from "../src/cache.ts";
import { AdaptiveBatchSizeRegulator } from "../src/regulator.ts";
import { VectorizedEngine, WideTable } from "../src/engine.ts";
import type { QueryResult } from "../src/engine.ts";

function pct(x: number): string {
  return (x * 100).toFixed(1) + "%";
}

function printTrace(title: string, result: QueryResult, maxRows = 12): void {
  console.log(`\n=== ${title} ===`);
  console.log(
    "batch | rows | batchSize | footprint | fitsL1 | L1 hit | L2 hit | memReads",
  );
  const trace =
    result.trace.length > maxRows
      ? [
          ...result.trace.slice(0, 6),
          null,
          ...result.trace.slice(-4),
        ]
      : result.trace;
  for (const t of trace) {
    if (t === null) {
      console.log("  ... |  ... |       ... |       ... |    ... |    ... |    ... |      ...");
      continue;
    }
    console.log(
      `${String(t.batchIndex).padStart(5)} | ${String(t.rows).padStart(4)} | ` +
        `${String(t.batchSize).padStart(9)} | ${String(t.footprintBytes).padStart(9)} | ` +
        `${String(t.fitsInL1).padStart(6)} | ${pct(t.l1HitRate).padStart(6)} | ` +
        `${pct(t.l2HitRate).padStart(6)} | ${String(t.memoryReads).padStart(8)}`,
    );
  }
  console.log(
    `最终批大小=${result.finalBatchSize}  估计停顿周期=${result.estimatedCycles}  checksum=${result.checksum}`,
  );
}

// 场景 1（验收边界）：超宽宽表，16 列 * 8B = 128B/行，4 趟扫描。
// 内存带宽受限环境：L2 仅 64KB，1024 行批（128KB）超出 L2，颠簸直达内存。
{
  const cache = new CacheHierarchy(32 * 1024, 64 * 1024);
  const regulator = new AdaptiveBatchSizeRegulator({
    l1CapacityBytes: cache.l1CapacityBytes,
  });
  const engine = new VectorizedEngine(cache, regulator);
  const table = new WideTable({ rowCount: 16384, columnCount: 16 });
  const result = engine.runQuery(table, { passes: 4 });
  printTrace("超宽宽表查询（128B/行，自适应开启）", result);
}

// 场景 1 对照组：固定 1024
{
  const engine = new VectorizedEngine(new CacheHierarchy(32 * 1024, 64 * 1024));
  const table = new WideTable({ rowCount: 16384, columnCount: 16 });
  const result = engine.runQuery(table, { passes: 4, fixedBatchSize: true });
  printTrace("超宽宽表查询（128B/行，固定 batch=1024 对照组）", result, 4);
}

// 场景 2：极窄表，1 列 * 8B = 8B/行
{
  const engine = new VectorizedEngine();
  const table = new WideTable({ rowCount: 65536, columnCount: 1 });
  const result = engine.runQuery(table, { passes: 4 });
  printTrace("极窄表查询（8B/行，自适应放大）", result);
}
