/**
 * 运行样例：500 个刚体同时运动，Broadphase 粗测筛出 12 对候选碰撞对。
 *
 * 运行：node examples/demo.ts   （需要 Node.js >= 23.6，原生支持 TypeScript）
 */
import { performance } from "node:perf_hooks";
import { Broadphase } from "../src/index.ts";
import {
  BODY_COUNT,
  EXPECTED_PAIR_COUNT,
  bodyAabb,
  bruteForcePairs,
  buildScene,
  stepScene,
} from "./scene.ts";

const FRAMES = 120;
const WARMUP = 20;

function percentile(sorted: number[], p: number): number {
  const idx = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[idx];
}

function main(): void {
  console.log("=== Broadphase 粗测演示 ===");
  console.log(`场景: ${BODY_COUNT} 个刚体同时运动, 预期重叠对: ${EXPECTED_PAIR_COUNT}`);

  const bodies = buildScene();
  const bp = new Broadphase<number>(0.1);
  for (const body of bodies) bp.add(body.id, bodyAabb(body));
  console.log(`动态 AABB 树构建完成: ${bp.size} 个代理, 树高 ${bp.treeHeight} (log2(${BODY_COUNT}) ≈ 9)`);

  const collectTimesUs: number[] = [];
  const frameTimesUs: number[] = [];
  const bruteTimesUs: number[] = [];
  let pairs: Array<[number, number]> = [];

  for (let frame = 0; frame < FRAMES + WARMUP; frame++) {
    stepScene(bodies);
    const frameStart = performance.now();
    for (const body of bodies) bp.move(body.id, bodyAabb(body));
    const collectStart = performance.now();
    pairs = bp.collectPairs();
    const collectEnd = performance.now();
    if (frame < WARMUP) continue; // JIT 预热帧不计入统计
    collectTimesUs.push((collectEnd - collectStart) * 1000);
    frameTimesUs.push((collectEnd - frameStart) * 1000);
    const bruteStart = performance.now();
    bruteForcePairs(bodies);
    bruteTimesUs.push((performance.now() - bruteStart) * 1000);
  }

  const brutePairs = bruteForcePairs(bodies);
  const stats = (values: number[]): string => {
    const sorted = [...values].sort((a, b) => a - b);
    const avg = values.reduce((a, b) => a + b, 0) / values.length;
    return `avg ${avg.toFixed(1)}us | p50 ${percentile(sorted, 50).toFixed(1)}us | p99 ${percentile(sorted, 99).toFixed(1)}us`;
  };

  console.log(`\n模拟 ${FRAMES} 帧 (全部刚体每帧运动):`);
  console.log(`  粗测产出候选对: ${pairs.length} 对 (暴力比对验证: ${brutePairs.length} 对)`);
  console.log(`  候选对列表: ${JSON.stringify(pairs)}`);
  console.log(`  collectPairs 筛选耗时:   ${stats(collectTimesUs)}`);
  console.log(`  单帧 (move + collect):   ${stats(frameTimesUs)}`);
  console.log(`  O(N^2) 暴力比对单帧:     ${stats(bruteTimesUs)}`);

  const ok = pairs.length === EXPECTED_PAIR_COUNT && brutePairs.length === EXPECTED_PAIR_COUNT;
  console.log(`\n${ok ? "OK" : "FAIL"}: 粗测筛出 ${pairs.length} 对候选, 移交窄测阶段执行精细几何相交`);
  process.exitCode = ok ? 0 : 1;
}

main();
