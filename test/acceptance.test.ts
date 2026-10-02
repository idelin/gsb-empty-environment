import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { performance } from "node:perf_hooks";
import { Broadphase } from "../src/broadphase.ts";
import {
  BODY_COUNT,
  EXPECTED_PAIR_COUNT,
  bodyAabb,
  bruteForcePairs,
  buildScene,
  stepScene,
} from "../examples/scene.ts";
import { pairKeys } from "./utils.ts";

function median(values: number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

describe("验收：500 刚体同时运动，粗测筛出 12 对候选", () => {
  it("每帧仅产出 12 对包围盒重叠候选，且与暴力比对一致", () => {
    const bodies = buildScene();
    assert.equal(bodies.length, BODY_COUNT);

    const bp = new Broadphase<number>(0.1);
    for (const body of bodies) bp.add(body.id, bodyAabb(body));

    const frames = 120;
    const collectTimesUs: number[] = [];
    const frameTimesUs: number[] = [];
    const bruteTimesUs: number[] = [];
    let lastPairs: Array<[number, number]> = [];

    // 前 20 帧作为 JIT 预热，不计入统计
    for (let frame = 0; frame < frames + 20; frame++) {
      stepScene(bodies);
      const frameStart = performance.now();
      for (const body of bodies) bp.move(body.id, bodyAabb(body));
      const collectStart = performance.now();
      lastPairs = bp.collectPairs();
      const collectEnd = performance.now();
      if (frame >= 20) {
        collectTimesUs.push((collectEnd - collectStart) * 1000);
        frameTimesUs.push((collectEnd - frameStart) * 1000);
        const bruteStart = performance.now();
        bruteForcePairs(bodies);
        bruteTimesUs.push((performance.now() - bruteStart) * 1000);
      }
    }

    // 1) 正确性：恰好 12 对，且与 O(N^2) 暴力比对完全一致
    assert.equal(lastPairs.length, EXPECTED_PAIR_COUNT);
    assert.deepEqual(pairKeys(lastPairs), pairKeys(bruteForcePairs(bodies)));

    // 2) 性能：取中位数以排除环境抖动；典型值为数十微秒，阈值放宽至 1ms 适配慢速 CI
    const p50Collect = median(collectTimesUs);
    const p50Frame = median(frameTimesUs);
    const p50Brute = median(bruteTimesUs);
    console.log(
      `  [perf] 500 刚体 x ${frames} 帧 (p50): ` +
        `collectPairs ${p50Collect.toFixed(1)}us, ` +
        `单帧(move+collect) ${p50Frame.toFixed(1)}us, ` +
        `暴力比对 ${p50Brute.toFixed(1)}us`,
    );
    assert.ok(p50Collect < 1000, `collectPairs p50 ${p50Collect.toFixed(1)}us 超过 1ms`);

    // 3) 对比：粗测显著优于 O(N^2) 暴力比对
    assert.ok(p50Frame < p50Brute, "broadphase 应快于暴力比对");
  });
});
