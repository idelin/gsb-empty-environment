import { test } from "node:test";
import assert from "node:assert/strict";
import { SlidingWindowHistogram } from "../src/slidingWindowHistogram.ts";

/** 确定性伪随机数（mulberry32），保证测试可复现 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function makeNormal(rng: () => number): () => number {
  return () => {
    const u1 = Math.max(rng(), 1e-12);
    const u2 = rng();
    return Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
  };
}

/** 长尾延迟：对数正态，median 毫秒 */
function makeLognormal(rng: () => number, median: number, sigma: number): () => number {
  const normal = makeNormal(rng);
  return () => median * Math.exp(sigma * normal());
}

/** 精确 nearest-rank 分位数（完全保留样本的基准实现） */
function exactQuantile(values: number[], q: number): number {
  const sorted = [...values].sort((a, b) => a - b);
  const rank = Math.max(1, Math.ceil(q * sorted.length));
  return sorted[rank - 1]!;
}

test("桶映射：估值误差在理论界限内", () => {
  const h = new SlidingWindowHistogram();
  const rng = mulberry32(42);
  for (let i = 0; i < 200_000; i++) {
    // 覆盖 0.001ms ~ 1h 的宽范围
    const v = 0.001 * Math.pow(10, rng() * 9);
    const idx = h.bucketIndex(v);
    const est = h.bucketEstimate(idx);
    const lo = h.bucketLowerBound(idx);
    const hi = h.bucketUpperBound(idx);
    assert.ok(lo <= v && v < hi, `bucket bounds contain value: v=${v} [${lo}, ${hi})`);
    const err = Math.abs(est - v);
    if (v < h.fixedRange) {
      assert.ok(err <= h.maxAbsoluteError + 1e-9, `fixed-region abs err ${err} <= ${h.maxAbsoluteError}`);
    } else {
      assert.ok(err / v <= h.maxRelativeError + 1e-12, `log-region rel err ${err / v} <= ${h.maxRelativeError}`);
    }
  }
});

test("滑动过期与反向补偿：窗口直方图与全新重建逐桶一致", () => {
  const capacity = 1000;
  const h = new SlidingWindowHistogram({ capacity });
  const rng = mulberry32(7);
  const next = makeLognormal(rng, 50, 0.8);
  const all: number[] = [];

  for (let i = 0; i < 5000; i++) {
    const v = next();
    h.record(v);
    all.push(v);
  }

  assert.equal(h.windowSize, capacity);
  assert.equal(h.totalRecorded, 5000);
  assert.equal(h.countSum(), capacity, "反向补偿后桶计数和应等于窗口大小");

  // 用最后 1000 个样本重建一个等效直方图，逐桶对比
  const rebuilt = new SlidingWindowHistogram({ capacity: 10_000 });
  for (const v of all.slice(-capacity)) rebuilt.record(v);
  for (let b = 0; b < rebuilt.buckets; b++) {
    assert.equal(h.countAt(b), rebuilt.countAt(b), `bucket ${b} count mismatch`);
  }
});

test("流式对照：每个阶段 P90/P99 与完全保留样本的误差 <= 0.5%", () => {
  const capacity = 1000;
  const h = new SlidingWindowHistogram({ capacity });
  const rng = mulberry32(1234);
  const next = makeLognormal(rng, 50, 0.8);
  const window: number[] = [];
  const TOLERANCE = 0.005;

  for (let i = 0; i < 6000; i++) {
    const v = next();
    h.record(v);
    window.push(v);
    if (window.length > capacity) window.shift();

    if (i >= 100 && i % 7 === 0) {
      for (const q of [0.9, 0.99]) {
        const est = h.quantile(q);
        const exact = exactQuantile(window, q);
        const relErr = Math.abs(est - exact) / exact;
        assert.ok(
          relErr <= TOLERANCE,
          `step ${i} q=${q}: est=${est.toFixed(3)} exact=${exact.toFixed(3)} relErr=${(relErr * 100).toFixed(4)}%`,
        );
      }
    }
  }
});

test("验收：极端毛刺滑出窗口后 P99 灵敏回落，且与精确值误差 <= 0.5%", () => {
  const capacity = 1000;
  const h = new SlidingWindowHistogram({ capacity });
  const rng = mulberry32(2026);
  const baseline = makeLognormal(rng, 50, 0.6);
  const window: number[] = [];

  const push = (v: number) => {
    h.record(v);
    window.push(v);
    if (window.length > capacity) window.shift();
  };
  const check = (label: string) => {
    const est = h.quantile(0.99);
    const exact = exactQuantile(window, 0.99);
    const relErr = Math.abs(est - exact) / exact;
    assert.ok(relErr <= 0.005, `${label}: est=${est} exact=${exact} relErr=${relErr}`);
    return { est, exact };
  };

  // 阶段 1：1000 个长尾基线样本，窗口填满
  for (let i = 0; i < capacity; i++) push(baseline());
  const before = check("baseline");

  // 阶段 2：连续压入 40 个极端毛刺（30 秒）
  for (let i = 0; i < 40; i++) push(30_000);
  const during = check("spike");
  assert.ok(during.est > 5_000, `毛刺期间 P99 应显著抬升，实际 ${during.est}`);

  // 阶段 3：再压入 1000 个基线样本，毛刺全部滑出窗口
  for (let i = 0; i < capacity; i++) push(baseline());
  const after = check("recovered");

  // 灵敏回落：恢复后的 P99 与毛刺前基线 P99 处于同一量级
  assert.ok(
    after.est < before.est * 2,
    `P99 应回落至基线附近：before=${before.est.toFixed(2)} after=${after.est.toFixed(2)}`,
  );
  assert.ok(after.est < during.est / 100, "回落幅度应超过两个数量级");
});

test("边界：空窗口 / 未装满 / capacity=1", () => {
  const h = new SlidingWindowHistogram({ capacity: 3 });
  assert.ok(Number.isNaN(h.p99));
  assert.equal(h.windowSize, 0);

  h.record(10);
  h.record(20);
  assert.equal(h.windowSize, 2);
  assert.equal(h.countSum(), 2);
  assert.equal(h.quantile(0.99), h.bucketEstimate(h.bucketIndex(20)));
  assert.equal(h.quantile(0.5), h.bucketEstimate(h.bucketIndex(10)));

  h.record(30);
  h.record(40); // 顶掉 10
  assert.equal(h.windowSize, 3);
  assert.equal(h.countSum(), 3);
  assert.equal(h.quantile(1), h.bucketEstimate(h.bucketIndex(40)));

  const single = new SlidingWindowHistogram({ capacity: 1 });
  single.record(5);
  single.record(5000);
  assert.equal(single.windowSize, 1);
  assert.equal(single.countSum(), 1);
  assert.equal(single.p99, single.bucketEstimate(single.bucketIndex(5000)));
});

test("性能冒烟：100 万次压入 + 每次 P99 查询在有限时间内完成", () => {
  const h = new SlidingWindowHistogram({ capacity: 1000 });
  const rng = mulberry32(99);
  const next = makeLognormal(rng, 50, 0.8);
  const start = performance.now();
  let sink = 0;
  for (let i = 0; i < 1_000_000; i++) {
    h.record(next());
    if (i % 1000 === 0) sink += h.p99;
  }
  const elapsed = performance.now() - start;
  assert.ok(Number.isFinite(sink));
  assert.ok(elapsed < 10_000, `1M pushes took ${elapsed.toFixed(0)}ms`);
  console.log(`    1M pushes + 1000 quantile queries: ${elapsed.toFixed(0)}ms`);
});
