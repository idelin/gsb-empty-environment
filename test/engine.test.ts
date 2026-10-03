import { describe, it } from "node:test";
import { strictEqual, ok } from "node:assert";
import { AdaptiveBatchSizeRegulator } from "../src/regulator";
import { CacheHierarchy } from "../src/cache-model";
import { VectorizedEngine, BatchTrace } from "../src/engine";

function runQuery(rowSizeBytes: number, rowCount = 16384) {
  const regulator = new AdaptiveBatchSizeRegulator();
  const cache = new CacheHierarchy();
  const engine = new VectorizedEngine(rowCount, rowSizeBytes, regulator, cache);
  const traces = engine.run();
  return { regulator, traces };
}

function batchSizeSequence(traces: BatchTrace[]): number[] {
  const seq: number[] = [];
  for (const t of traces) {
    if (seq.length === 0 || seq[seq.length - 1] !== t.batchSize) seq.push(t.batchSize);
  }
  return seq;
}

/** 去掉最后一个可能不足一批的尾批，避免误判为调节动作。 */
function fullBatches(traces: BatchTrace[]): BatchTrace[] {
  const last = traces[traces.length - 1];
  const maxSize = Math.max(...traces.map((t) => t.batchSize));
  return last.batchSize < maxSize ? traces.slice(0, -1) : traces;
}

describe("验收边界：超宽宽表查询（行宽 128B）", () => {
  const { regulator, traces } = runQuery(128);

  it("调节器感知内存带宽受限，将批量从 1024 动态下调至 256", () => {
    strictEqual(traces[0].batchSize, 1024);
    const seq = batchSizeSequence(traces);
    strictEqual(seq[0], 1024);
    ok(seq.includes(512), "应经过 512 过渡");
    strictEqual(seq[seq.length - 1], 256);
    strictEqual(regulator.currentBatchSize, 256);
  });

  it("256 行单批恰好容纳在 32KiB L1 缓存内", () => {
    strictEqual(256 * 128, 32 * 1024);
    strictEqual(regulator.capacityBoundFor(128), 256);
  });

  it("调整后 L1 命中率恢复热态（>= 0.85），避免缓存颠簸", () => {
    const tail = traces.slice(-8);
    for (const t of tail) ok(t.l1HitRate >= 0.85, `L1 命中率 ${t.l1HitRate}`);
  });

  it("调整后每行处理成本显著下降", () => {
    const before = traces[0].cyclesPerRow;
    const after = traces[traces.length - 1].cyclesPerRow;
    ok(after < before * 0.7, `before=${before} after=${after}`);
  });
});

describe("行极窄的查询（行宽 8B，单列投影）", () => {
  const { regulator, traces } = runQuery(8);

  it("缓存保持热态，批量放大到 4096", () => {
    strictEqual(regulator.currentBatchSize, 4096);
    strictEqual(batchSizeSequence(fullBatches(traces)).join(","), "1024,2048,4096");
  });
});

describe("中等行宽的查询（行宽 32B）", () => {
  const { regulator, traces } = runQuery(32);

  it("初始 1024 恰好等于 L1 容量上界，批量保持稳定", () => {
    strictEqual(regulator.currentBatchSize, 1024);
    ok(batchSizeSequence(traces).every((s) => s === 1024));
  });
});
