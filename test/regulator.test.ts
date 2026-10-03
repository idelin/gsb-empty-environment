import { describe, it } from "node:test";
import { strictEqual, ok } from "node:assert";
import { AdaptiveBatchSizeRegulator, CacheStats, floorPow2 } from "../src/regulator";

function stats(l1HitRate: number, total = 8000): CacheStats {
  const l1Hits = Math.round(total * l1HitRate);
  const l1Misses = total - l1Hits;
  return { l1Hits, l1Misses, l2Hits: l1Misses, l2Misses: 0 };
}

describe("floorPow2", () => {
  it("向下取 2 的幂", () => {
    strictEqual(floorPow2(1), 1);
    strictEqual(floorPow2(255), 128);
    strictEqual(floorPow2(256), 256);
    strictEqual(floorPow2(1000), 512);
    strictEqual(floorPow2(0), 1);
  });
});

describe("AdaptiveBatchSizeRegulator.capacityBoundFor", () => {
  const reg = new AdaptiveBatchSizeRegulator();

  it("行宽 128B 时容量上界为 256（256 行恰好装满 32KiB L1）", () => {
    strictEqual(reg.capacityBoundFor(128), 256);
  });

  it("行宽 32B 时容量上界为 1024", () => {
    strictEqual(reg.capacityBoundFor(32), 1024);
  });

  it("行极窄（8B / 4B）时容量上界顶到 maxBatchSize 4096", () => {
    strictEqual(reg.capacityBoundFor(8), 4096);
    strictEqual(reg.capacityBoundFor(4), 4096);
  });

  it("行超宽（大于 L1）时容量上界夹到 minBatchSize 256", () => {
    strictEqual(reg.capacityBoundFor(64 * 1024), 256);
  });
});

describe("AdaptiveBatchSizeRegulator 收缩行为（宽行 / 缓存颠簸）", () => {
  it("L1 命中率过低时从 1024 逐步减半到 256 并停住", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    strictEqual(reg.currentBatchSize, 1024);

    strictEqual(reg.observe(stats(0.0), 128), 512);
    strictEqual(reg.observe(stats(0.0), 128), 256);
    strictEqual(reg.observe(stats(0.0), 128), 256);

    const reasons = reg.history.map((h) => h.reason);
    ok(reasons.includes("cache-thrashing-shrink"));
  });

  it("批量永远不会低于 minBatchSize 256", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    for (let i = 0; i < 10; i++) reg.observe(stats(0.0), 128);
    strictEqual(reg.currentBatchSize, 256);
    ok(reg.history.every((h) => h.to >= 256));
  });

  it("命中率尚可但批量超出 L1 容量时直接收缩到容量上界", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    // 行宽 128B -> 容量上界 256；命中率处于中间带，不触发颠簸分支。
    strictEqual(reg.observe(stats(0.7), 128), 256);
    strictEqual(reg.history[reg.history.length - 1].reason, "capacity-shrink");
  });
});

describe("AdaptiveBatchSizeRegulator 放大行为（窄行 / 缓存热态）", () => {
  it("L1 命中率高时从 1024 翻倍放大到 4096 并封顶", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    strictEqual(reg.observe(stats(0.95), 8), 2048);
    strictEqual(reg.observe(stats(0.95), 8), 4096);
    strictEqual(reg.observe(stats(0.95), 8), 4096);
    ok(reg.history.every((h) => h.to <= 4096));
  });

  it("放大不超过 L1 容量上界", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    // 行宽 32B -> 容量上界 1024，初始即 1024，不应继续放大。
    strictEqual(reg.observe(stats(0.99), 32), 1024);
  });
});

describe("AdaptiveBatchSizeRegulator 稳定保持", () => {
  it("命中率处于水位之间且批量不超容量时保持不动", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    strictEqual(reg.observe(stats(0.75), 32), 1024);
    strictEqual(reg.history[reg.history.length - 1].reason, "hold");
  });

  it("历史记录包含 from/to/reason/hitRate 等观测信息", () => {
    const reg = new AdaptiveBatchSizeRegulator();
    reg.observe(stats(0.1), 128);
    const last = reg.history[reg.history.length - 1];
    strictEqual(last.from, 1024);
    strictEqual(last.to, 512);
    strictEqual(last.rowSizeBytes, 128);
    strictEqual(last.capacityBound, 256);
    ok(last.l1HitRate < 0.6);
  });
});
