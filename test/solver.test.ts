import type { Problem } from '../src/types.ts';
import { solve } from '../src/solver.ts';
import { buildFlows } from '../src/flow.ts';
import { sampleProblem } from '../src/sample.ts';

let failures = 0;

function check(cond: boolean, msg: string): void {
  if (!cond) {
    failures++;
    console.error(`FAIL: ${msg}`);
  }
}

/** 可复现随机数 */
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

function randomProblem(rand: () => number, n: number): Problem {
  const parts = ['P0', 'P1', 'P2', 'P3'].slice(0, 2 + Math.floor(rand() * 3));
  const pick = (): Record<string, number> => {
    const out: Record<string, number> = {};
    for (const p of parts) if (rand() < 0.45) out[p] = 1 + Math.floor(rand() * 3);
    return out;
  };
  const stock: Record<string, number> = {};
  for (const p of parts) stock[p] = Math.floor(rand() * 5);
  return {
    budget: 10 + Math.floor(rand() * 50),
    recoveryYield: 0.5 + Math.floor(rand() * 6) * 0.1,
    stock,
    devices: Array.from({ length: n }, (_, i) => ({
      id: `M${i}`,
      repairCost: 1 + Math.floor(rand() * 20),
      repairValue: Math.floor(rand() * 40),
      residualValue: Math.floor(rand() * 10),
      repairParts: pick(),
      salvageParts: pick(),
    })),
  };
}

/** 独立实现的 3^n 暴力枚举，作为正确性基准 */
function bruteForce(problem: Problem): number {
  const n = problem.devices.length;
  const yieldOf = (p: string): number => problem.partYield?.[p] ?? problem.recoveryYield;
  let best = 0; // 全闲置恒可行，目标为 0
  const assign = new Array<number>(n);

  function feasibleAndValue(): number {
    let cost = 0;
    let value = 0;
    const demand = new Map<string, number>();
    const supply = new Map<string, number>();
    for (const [p, q] of Object.entries(problem.stock)) supply.set(p, q);
    for (let i = 0; i < n; i++) {
      const d = problem.devices[i];
      if (assign[i] === 1) {
        cost += d.repairCost;
        value += d.repairValue;
        for (const [p, q] of Object.entries(d.repairParts)) {
          demand.set(p, (demand.get(p) ?? 0) + q);
        }
      } else if (assign[i] === 2) {
        value -= d.residualValue;
        for (const [p, q] of Object.entries(d.salvageParts)) {
          supply.set(p, (supply.get(p) ?? 0) + Math.floor(q * yieldOf(p) + 1e-9));
        }
      }
    }
    if (cost > problem.budget) return -Infinity;
    for (const [p, q] of demand) {
      if (q > (supply.get(p) ?? 0)) return -Infinity;
    }
    return value;
  }

  function dfs(i: number): void {
    if (i === n) {
      best = Math.max(best, feasibleAndValue());
      return;
    }
    for (let a = 0; a < 3; a++) {
      assign[i] = a;
      dfs(i + 1);
    }
  }
  dfs(0);
  return best;
}

function verifyFeasible(problem: Problem): void {
  const result = solve(problem);
  const { flows, balance } = buildFlows(problem, result);

  // 互斥且全覆盖
  const total = result.repaired.length + result.dismantled.length + result.idle.length;
  check(total === problem.devices.length, '设备决策未覆盖全部设备');
  check(new Set([...result.repaired, ...result.dismantled, ...result.idle]).size === total,
    '修复/拆解/闲置存在重叠（违反互斥）');

  // 预算约束
  check(result.budgetUsed <= problem.budget + 1e-9,
    `预算超支: ${result.budgetUsed} > ${problem.budget}`);

  // 目标值自洽
  check(Math.abs(result.objective - (result.valueRestored - result.residualLost)) < 1e-9,
    '目标值与 恢复价值-残值损失 不一致');

  // 备件守恒：消耗 <= 库存 + 回收，剩余 >= 0
  for (const b of balance) {
    check(b.consumed <= b.stock + b.salvaged + 1e-9, `备件 ${b.part} 消耗超过供给`);
    check(b.leftover >= 0, `备件 ${b.part} 剩余为负`);
    check(Math.abs(b.leftover - (b.stock + b.salvaged - b.consumed)) < 1e-9,
      `备件 ${b.part} 平衡不守恒`);
  }

  // 流水满足每台修复设备的齐套需求
  const delivered = new Map<string, number>();
  for (const f of flows) {
    delivered.set(`${f.to}|${f.part}`, (delivered.get(`${f.to}|${f.part}`) ?? 0) + f.qty);
  }
  for (const d of problem.devices) {
    if (!result.repaired.includes(d.id)) continue;
    for (const [p, q] of Object.entries(d.repairParts)) {
      check((delivered.get(`${d.id}|${p}`) ?? 0) === q,
        `设备 ${d.id} 的备件 ${p} 未齐套`);
    }
  }
}

// 1) 小规模随机实例：分支定界 vs 暴力枚举
const rand = mulberry32(20261005);
let compared = 0;
for (let t = 0; t < 300; t++) {
  const n = 4 + Math.floor(rand() * 6); // 4..9 台
  const problem = randomProblem(rand, n);
  const expected = bruteForce(problem);
  const result = solve(problem);
  check(result.status === 'optimal', `实例 ${t} 未证最优`);
  if (Math.abs(result.objective - expected) > 1e-9) {
    failures++;
    console.error(`FAIL: 实例 ${t} 目标值 ${result.objective} != 暴力解 ${expected}`);
    console.error(JSON.stringify(problem));
  } else {
    compared++;
  }
}
console.log(`对拍通过 ${compared}/300 个随机实例`);

// 2) 内置样例：可行性与流水守恒
verifyFeasible(sampleProblem);
const sampleResult = solve(sampleProblem);
console.log(`样例目标值 ${sampleResult.objective}，节点 ${sampleResult.nodes}，状态 ${sampleResult.status}`);

// 3) 18 台设备随机实例：限时内给出可行解
{
  const big = randomProblem(mulberry32(777), 18);
  const started = Date.now();
  const result = solve(big, { timeLimitMs: 10_000 });
  const elapsed = Date.now() - started;
  console.log(`18 台实例: 状态 ${result.status}，目标值 ${result.objective}，节点 ${result.nodes}，耗时 ${elapsed}ms`);
  check(elapsed < 15_000, '18 台实例求解超时');
  verifyFeasible(big);
}

// 4) 边界：零设备 / 超预算设备 / 回收率 1
{
  const empty: Problem = { budget: 10, stock: {}, recoveryYield: 0.8, devices: [] };
  check(solve(empty).objective === 0, '零设备实例目标应为 0');

  const tooExpensive: Problem = {
    budget: 5, stock: {}, recoveryYield: 1,
    devices: [{ id: 'X', repairCost: 10, repairValue: 100, residualValue: 1,
      repairParts: {}, salvageParts: {} }],
  };
  const r = solve(tooExpensive);
  check(r.repaired.length === 0 && r.objective === 0, '超预算设备不应被修复');

  const fullYield: Problem = {
    budget: 10, stock: {}, recoveryYield: 1,
    devices: [
      { id: 'A', repairCost: 5, repairValue: 50, residualValue: 1,
        repairParts: { 板: 1 }, salvageParts: {} },
      { id: 'B', repairCost: 5, repairValue: 1, residualValue: 2,
        repairParts: {}, salvageParts: { 板: 1 } },
    ],
  };
  const r2 = solve(fullYield);
  check(r2.repaired.includes('A') && r2.dismantled.includes('B') && r2.objective === 48,
    `拆 B 修 A 场景错误: ${JSON.stringify(r2)}`);
}

if (failures > 0) {
  console.error(`\n${failures} 项检查失败`);
  process.exitCode = 1;
} else {
  console.log('\n全部检查通过');
}
