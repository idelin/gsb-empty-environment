import { test } from "node:test";
import assert from "node:assert/strict";
import { performance } from "node:perf_hooks";
import { SpacetimeGridIndex } from "../src/grid_index.ts";
import { IntersectionProver } from "../src/prover.ts";
import type { TrajectoryPoint } from "../src/types.ts";

/** 确定性伪随机数（xorshift32），保证测试可复现。 */
function makeRng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state ^= state << 13;
    state ^= state >>> 17;
    state ^= state << 5;
    return (state >>> 0) / 0xffffffff;
  };
}

const ENTITY_COUNT = 500;
const POINTS_PER_ENTITY = 1440; // 每分钟一个点，共 24 小时
const QUERY_COUNT = 200;

test("性能：500 实体 × 1440 点规模下，单次时空求交平均耗时为毫秒级", () => {
  const rng = makeRng(42);
  const index = new SpacetimeGridIndex({
    sliceDurationMs: 5 * 60_000,
    cellSizeMeters: 20,
  });
  const base = Date.parse("2026-09-30T00:00:00+08:00");

  const buildStart = performance.now();
  for (let e = 0; e < ENTITY_COUNT; e++) {
    const points: TrajectoryPoint[] = [];
    let x = rng() * 1000;
    let y = rng() * 1000;
    for (let i = 0; i < POINTS_PER_ENTITY; i++) {
      x += (rng() - 0.5) * 40;
      y += (rng() - 0.5) * 40;
      points.push({ t: base + i * 60_000, x, y, z: 0 });
    }
    index.insert(`entity-${e}`, points);
  }
  const buildMs = performance.now() - buildStart;

  const prover = new IntersectionProver(index);
  // 轨迹入库时承诺先行公开存证（一次性成本），查询期直接复用。
  const commitStart = performance.now();
  prover.precommit();
  const commitMs = performance.now() - commitStart;
  // 预热 JIT。
  for (let i = 0; i < 20; i++) {
    prover.prove(`entity-${i}`, `entity-${i + 1}`, 15);
  }

  const queryStart = performance.now();
  let totalIntervals = 0;
  for (let i = 0; i < QUERY_COUNT; i++) {
    const a = Math.floor(rng() * ENTITY_COUNT);
    const b = Math.floor(rng() * ENTITY_COUNT);
    if (a === b) continue;
    const proof = prover.prove(`entity-${a}`, `entity-${b}`, 15);
    totalIntervals += proof.intervals.length;
  }
  const totalMs = performance.now() - queryStart;
  const avgMs = totalMs / QUERY_COUNT;

  console.log(
    `    [性能] 建索引 ${buildMs.toFixed(1)}ms（${ENTITY_COUNT * POINTS_PER_ENTITY} 点）；` +
      `预提交承诺 ${commitMs.toFixed(1)}ms；` +
      `${QUERY_COUNT} 次求交共 ${totalMs.toFixed(1)}ms，平均 ${avgMs.toFixed(3)}ms/次，` +
      `共检出 ${totalIntervals} 段相遇`,
  );
  assert.ok(avgMs < 10, `平均求交耗时 ${avgMs}ms 超出 10ms 预算`);
});
