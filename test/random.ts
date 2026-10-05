// 随机对拍：n <= 10 与暴力解比较；n = 18 做合法性与性能抽查。

import { planRelease } from '../src/planner';
import { bruteForceMinBatches } from '../src/brute';
import type { PlanRequest } from '../src/model';
import { assert, checkInstance, componentsOf, validatePlan } from './helpers';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomRequest(rand: () => number, n: number, edgeProb: number, capacity: number): PlanRequest {
  const projects = [];
  for (let i = 0; i < n; i++) projects.push({ id: 'p' + i, deps: [] as string[] });
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (rand() < edgeProb) projects[i].deps.push('p' + j);
    }
  }
  return { capacity, projects };
}

export function run(): void {
  const t0 = Date.now();
  const rand = mulberry32(20261005);

  // 小规模随机对拍
  let checked = 0;
  for (let iter = 0; iter < 3000; iter++) {
    const n = 1 + Math.floor(rand() * 10);
    const edgeProb = [0.05, 0.15, 0.3, 0.5][Math.floor(rand() * 4)];
    const cap = 1 + Math.floor(rand() * 5);
    const req = randomRequest(rand, n, edgeProb, cap);
    const comp = componentsOf(req);
    const brute = bruteForceMinBatches(comp.sizes, comp.predMask, cap);
    checkInstance(req, brute, `random#${iter} n=${n} p=${edgeProb} cap=${cap}`);
    checked++;
  }
  console.log(`random: ${checked} 个小规模实例对拍通过`);

  // 上限规模 n = 18：合法性与性能（无暴力对照，用独立校验）
  let maxMs = 0;
  for (let iter = 0; iter < 200; iter++) {
    const edgeProb = [0.05, 0.1, 0.2, 0.4][Math.floor(rand() * 4)];
    const cap = 1 + Math.floor(rand() * 18);
    const req = randomRequest(rand, 18, edgeProb, cap || 1);
    const start = Date.now();
    const result = planRelease(req);
    const elapsed = Date.now() - start;
    if (elapsed > maxMs) maxMs = elapsed;
    if (result.ok) {
      const err = validatePlan(req, result);
      assert(err === null, `n=18 #${iter}: 计划无效: ${err}`);
    }
  }

  // 极端形状：空图 / 完全图 / 长链，n = 18
  const empty = randomRequest(rand, 18, 0, 9);
  assert(planRelease(empty).ok, '空图应可行');
  const full: PlanRequest = {
    capacity: 9,
    projects: Array.from({ length: 18 }, (_, i) => ({
      id: 'q' + i,
      deps: Array.from({ length: 18 }, (_, j) => 'q' + j).filter((x) => x !== 'q' + i),
    })),
  };
  const fullRes = planRelease(full);
  assert(!fullRes.ok, '18 完全图容量 9 应不可行');
  const chainReq: PlanRequest = {
    capacity: 3,
    projects: Array.from({ length: 18 }, (_, i) => ({
      id: 'c' + i,
      deps: i + 1 < 18 ? ['c' + (i + 1)] : [],
    })),
  };
  const chainRes = planRelease(chainReq);
  assert(chainRes.ok && chainRes.batchCount === 18, '18 链需 18 批');

  // 对抗性最坏情形：空图（DP 分支最多）各容量
  for (const cap of [1, 2, 5, 9, 17, 18]) {
    const req: PlanRequest = {
      capacity: cap,
      projects: Array.from({ length: 18 }, (_, i) => ({ id: 'e' + i, deps: [] })),
    };
    const start = Date.now();
    const r = planRelease(req);
    const elapsed = Date.now() - start;
    assert(r.ok && r.batchCount === Math.ceil(18 / cap), `空图 cap=${cap} 批次数`);
    assert(elapsed < 5000, `空图 cap=${cap} 耗时 ${elapsed}ms 超限`);
  }
  // 9 个二元环，容量 2 => 9 批
  const pairs: PlanRequest = {
    capacity: 2,
    projects: Array.from({ length: 18 }, (_, i) => ({
      id: 'p' + i,
      deps: ['p' + (i % 2 === 0 ? i + 1 : i - 1)],
    })),
  };
  const pairsRes = planRelease(pairs);
  assert(pairsRes.ok && pairsRes.batchCount === 9, '9 个二元环容量 2 需 9 批');

  console.log(`random: n=18 抽查 200 例通过，单例最大耗时 ${maxMs}ms，总耗时 ${Date.now() - t0}ms`);
}
