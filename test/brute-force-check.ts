/**
 * 交叉验证：对小规模随机用例，用独立的“集合划分枚举”暴力法求最优解，
 * 与 src/solver.ts 的子集 DP 结果对比；同时对方案做结构性校验。
 *
 * 运行：node test/brute-force-check.ts
 */
import { solveCuttingStock, type PlannerInput, type PlanResult } from '../src/solver.ts';

const EPS = 1e-9;

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

/** 独立重写的单棒可行性/刀数/余料（不依赖 solver 实现）。 */
function fitCheck(
  lens: number[],
  L: number,
  kerf: number,
): { cuts: number; remnant: number } | null {
  const k = lens.length;
  const sum = lens.reduce((a, b) => a + b, 0);
  if (Math.abs(sum + (k - 1) * kerf - L) <= EPS) return { cuts: k - 1, remnant: 0 };
  if (sum + k * kerf <= L + EPS) return { cuts: k, remnant: Math.max(0, L - sum - k * kerf) };
  return null;
}

interface Key {
  cost: number;
  cuts: number;
  bars: number;
}

function better(a: Key, b: Key): boolean {
  if (Math.abs(a.cost - b.cost) > EPS) return a.cost < b.cost;
  if (a.cuts !== b.cuts) return a.cuts < b.cuts;
  return a.bars < b.bars;
}

/** 暴力法：递归枚举订单件集合的所有划分，每块选最优原料棒。 */
function bruteForce(input: PlannerInput): Key | null {
  const pieces: number[] = [];
  for (const d of input.demands) for (let i = 0; i < d.count; i++) pieces.push(d.length);
  const n = pieces.length;

  // 每块的最优 (price, cuts)
  const blockBest = new Map<number, { price: number; cuts: number } | null>();
  function bestForBlock(mask: number): { price: number; cuts: number } | null {
    if (blockBest.has(mask)) return blockBest.get(mask)!;
    const lens: number[] = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) lens.push(pieces[i]);
    let best: { price: number; cuts: number } | null = null;
    for (const s of input.stocks) {
      const f = fitCheck(lens, s.length, input.kerf);
      if (!f) continue;
      if (
        !best ||
        s.price < best.price - EPS ||
        (Math.abs(s.price - best.price) <= EPS && f.cuts < best.cuts)
      ) {
        best = { price: s.price, cuts: f.cuts };
      }
    }
    blockBest.set(mask, best);
    return best;
  }

  // 递归：取最低位件，枚举包含它的块
  const memo = new Map<number, Key | null>();
  function rec(mask: number): Key | null {
    if (mask === 0) return { cost: 0, cuts: 0, bars: 0 };
    const hit = memo.get(mask);
    if (hit !== undefined) return hit;
    const low = mask & -mask;
    let best: Key | null = null;
    for (let sub = mask; sub > 0; sub = (sub - 1) & mask) {
      if ((sub & low) === 0) continue;
      const blk = bestForBlock(sub);
      if (!blk) continue;
      const rest = rec(mask ^ sub);
      if (!rest) continue;
      const cand: Key = {
        cost: rest.cost + blk.price,
        cuts: rest.cuts + blk.cuts,
        bars: rest.bars + 1,
      };
      if (!best || better(cand, best)) best = cand;
    }
    memo.set(mask, best);
    return best;
  }
  return rec((1 << n) - 1);
}

/** 结构性校验：方案本身必须合法且自洽。 */
function validatePlan(input: PlannerInput, plan: PlanResult): string[] {
  const errors: string[] = [];
  if (!plan.feasible) return errors;
  const demandPieces: number[] = [];
  for (const d of input.demands) for (let i = 0; i < d.count; i++) demandPieces.push(d.length);
  const planPieces: number[] = [];
  let cost = 0;
  let cuts = 0;
  for (const bar of plan.bars) {
    planPieces.push(...bar.pieces);
    cost += bar.stock.price;
    cuts += bar.cuts;
    const f = fitCheck(bar.pieces, bar.stock.length, input.kerf);
    if (!f) {
      errors.push(`棒(${bar.stock.length}) 切 ${bar.pieces} 不可行`);
      continue;
    }
    if (f.cuts !== bar.cuts) errors.push(`刀数不符：报 ${bar.cuts} 应为 ${f.cuts}`);
    if (Math.abs(f.remnant - bar.remnant) > EPS) {
      errors.push(`余料不符：报 ${bar.remnant} 应为 ${f.remnant}（锯缝不得计入）`);
    }
    const fate = bar.remnant >= plan.minUsefulRemnant - EPS ? 'reusable' : 'scrap';
    if (fate !== bar.remnantFate) errors.push(`余料去向标注错误：${bar.remnant}`);
  }
  const sortNum = (a: number[]) => [...a].sort((x, y) => x - y);
  if (JSON.stringify(sortNum(planPieces)) !== JSON.stringify(sortNum(demandPieces))) {
    errors.push('切出的订单件与需求不一致');
  }
  if (Math.abs(cost - plan.totalCost) > EPS) errors.push('总成本汇总不符');
  if (cuts !== plan.totalCuts) errors.push('总刀数汇总不符');
  if (plan.bars.length !== plan.totalBars) errors.push('根数汇总不符');
  return errors;
}

function randomCase(rnd: () => number, maxPieces: number): PlannerInput {
  const ri = (lo: number, hi: number) => lo + Math.floor(rnd() * (hi - lo + 1));
  const kerf = ri(0, 4);
  const stockCount = ri(1, 3);
  const stocks = Array.from({ length: stockCount }, () => {
    const length = ri(20, 120);
    return { length, price: ri(1, 10) * 5 + Math.round(length / 10) };
  });
  const demands: { length: number; count: number }[] = [];
  let total = 0;
  const groups = ri(1, 4);
  for (let g = 0; g < groups; g++) {
    const count = ri(1, 4);
    if (total + count > maxPieces) break;
    total += count;
    demands.push({ length: ri(5, 60), count });
  }
  return { kerf, stocks, demands };
}

let failed = 0;
const CASES = 400;
for (let seed = 1; seed <= CASES; seed++) {
  const rnd = mulberry32(seed);
  const input = randomCase(rnd, 10);
  const plan = solveCuttingStock(input);
  const expect = bruteForce(input);

  const structural = validatePlan(input, plan);
  if (structural.length > 0) {
    failed++;
    console.error(`用例 ${seed} 结构校验失败:`, structural, JSON.stringify(input));
    continue;
  }
  if (!expect) {
    if (plan.feasible) {
      failed++;
      console.error(`用例 ${seed} 应为不可行，但求解器给出了方案`, JSON.stringify(input));
    }
    continue;
  }
  if (!plan.feasible) {
    failed++;
    console.error(`用例 ${seed} 可行但求解器报不可行: ${plan.reason}`, JSON.stringify(input));
    continue;
  }
  const got: Key = { cost: plan.totalCost, cuts: plan.totalCuts, bars: plan.totalBars };
  if (
    Math.abs(got.cost - expect.cost) > EPS ||
    got.cuts !== expect.cuts ||
    got.bars !== expect.bars
  ) {
    failed++;
    console.error(
      `用例 ${seed} 最优性不符：DP=${JSON.stringify(got)} 暴力=${JSON.stringify(expect)}`,
      JSON.stringify(input),
    );
  }
}

if (failed === 0) {
  console.log(`全部 ${CASES} 个随机用例通过：结构合法，且 (成本, 刀数, 根数) 与暴力最优一致`);
} else {
  console.error(`${failed}/${CASES} 个用例失败`);
  process.exit(1);
}
