/**
 * 运行样例：模拟向量化执行引擎在不同行宽下的查询执行，
 * 展示自适应批大小调节器的动态调节过程。
 */
import { AdaptiveBatchSizeRegulator } from "../src/regulator";
import { CacheHierarchy } from "../src/cache-model";
import { VectorizedEngine, BatchTrace } from "../src/engine";

function pct(x: number): string {
  return (x * 100).toFixed(1).padStart(5) + "%";
}

function runScenario(title: string, rowSizeBytes: number, rowCount: number): void {
  console.log(`\n=== ${title} ===`);
  console.log(
    `行宽=${rowSizeBytes}B 行数=${rowCount} L1=32KiB L2=256KiB 初始Batch=1024 流水线算子数=8`,
  );

  const regulator = new AdaptiveBatchSizeRegulator();
  const cache = new CacheHierarchy();
  const engine = new VectorizedEngine(rowCount, rowSizeBytes, regulator, cache);
  const allTraces = engine.run();

  // 最后一个可能不足一批的尾批不参与展示与汇总。
  const maxSize = Math.max(...allTraces.map((t) => t.batchSize));
  const traces = allTraces.filter(
    (t, i) => i < allTraces.length - 1 || t.batchSize === maxSize,
  );

  console.log(
    "批次   | 批量  | L1命中率 | L2命中率 | 周期/行 | 调节动作",
  );
  let prev: BatchTrace | undefined;
  for (const t of traces) {
    const changed = !prev || prev.nextBatchSize !== t.nextBatchSize || prev.batchSize !== t.batchSize;
    if (t.batchIndex < 4 || changed || t.batchIndex === traces.length - 1) {
      const decision = regulator.history[t.batchIndex + 1];
      console.log(
        `#${String(t.batchIndex).padStart(5)} | ${String(t.batchSize).padStart(5)} | ` +
          `${pct(t.l1HitRate)}   | ${pct(t.l2HitRate)}   | ${t.cyclesPerRow.toFixed(1).padStart(7)} | ` +
          `${decision.reason} -> ${decision.to}`,
      );
    }
    prev = t;
  }

  const first = traces[0];
  const last = traces[traces.length - 1];
  console.log(
    `结果: 批量 ${first.batchSize} -> ${last.batchSize}，` +
      `L1命中率 ${pct(first.l1HitRate)} -> ${pct(last.l1HitRate)}，` +
      `每行成本 ${first.cyclesPerRow.toFixed(1)} -> ${last.cyclesPerRow.toFixed(1)} 周期`,
  );
}

// 场景 A（验收边界）：超宽宽表查询，128B/行（16 个 int64 列）。
// 1024 行/批 = 128KiB，远超 32KiB L1 -> 缓存颠簸、内存带宽受限；
// 调节器应动态下调到 256 行/批 = 32KiB，恰好装满 L1。
runScenario("场景 A：超宽宽表查询（行宽 128B）", 128, 16384);

// 场景 B：行极窄（8B，单列投影），缓存热态，批量应放大到 4096。
runScenario("场景 B：极窄行查询（行宽 8B）", 8, 65536);

// 场景 C：中等行宽（32B），1024 行恰好装满 L1，批量保持稳定。
runScenario("场景 C：中等行宽查询（行宽 32B）", 32, 16384);
