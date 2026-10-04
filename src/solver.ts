import type { Decision, Problem, Solution } from "./types.ts";
import { buildFlows } from "./flow.ts";

export const MAX_DEVICES = 18;

const REPAIR = 0;
const DISMANTLE = 1;
const IDLE = 2;

function assertNonNegativeInt(value: number, what: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${what} 必须是非负整数，得到: ${value}`);
  }
}

/** 校验输入并返回规范化的备件名列表。 */
function validate(problem: Problem): string[] {
  if (problem.devices.length > MAX_DEVICES) {
    throw new Error(`设备数量 ${problem.devices.length} 超过上限 ${MAX_DEVICES}`);
  }
  assertNonNegativeInt(problem.budget, "预算");
  const rate = problem.recoveryRate ?? 1;
  if (rate < 0 || rate > 1) throw new Error(`全局回收率须在 [0,1] 内: ${rate}`);

  const names = new Set<string>();
  for (const [part, spec] of Object.entries(problem.parts)) {
    names.add(part);
    assertNonNegativeInt(spec.stock, `备件 ${part} 库存`);
    if (spec.price !== undefined) assertNonNegativeInt(spec.price, `备件 ${part} 单价`);
  }
  const ids = new Set<string>();
  for (const d of problem.devices) {
    if (ids.has(d.id)) throw new Error(`设备 id 重复: ${d.id}`);
    ids.add(d.id);
    assertNonNegativeInt(d.repairCost, `${d.id} 修复费`);
    assertNonNegativeInt(d.repairedValue, `${d.id} 修复价值`);
    assertNonNegativeInt(d.residualValue, `${d.id} 残值`);
    assertNonNegativeInt(d.dismantleCost ?? 0, `${d.id} 拆解费`);
    const r = d.recoveryRate ?? rate;
    if (r < 0 || r > 1) throw new Error(`${d.id} 回收率须在 [0,1] 内: ${r}`);
    for (const bag of [d.needs, d.contains]) {
      for (const [part, qty] of Object.entries(bag)) {
        names.add(part);
        assertNonNegativeInt(qty, `${d.id} 备件 ${part} 数量`);
      }
    }
  }
  return [...names].sort();
}

/**
 * 精确求解：在预算内选择每台设备的处置方式（修复 / 拆解 / 闲置），
 * 使方案总价值最大。分支限界 + 记忆化，18 台规模可秒解。
 *
 * 模型要点：
 * - 修复须备件齐套：库存 + 拆解回收 + 外购（若该备件有单价）之和须覆盖需求。
 *   搜索时备件余额允许为负（欠账），叶节点统一结算：缺口按单价外购，
 *   不可外购的备件出现缺口则方案不可行。欠账记账与决策顺序无关，保证精确性；
 * - 拆解与修复互斥（每台设备三选一），拆解损失残值、回收有损耗（floor 向下取整）；
 * - 不能循环产料：回收备件只流向被修复设备，被修复设备不得再拆解，
 *   因此备件流向天然是“来源(库存/采购/拆解机) -> 用途(修复机)”的有向无环图。
 */
export function solve(problem: Problem): Solution {
  const partNames = validate(problem);
  const P = partNames.length;
  const partIndex = new Map(partNames.map((name, i) => [name, i]));
  const globalRate = problem.recoveryRate ?? 1;

  const stock = partNames.map((p) => problem.parts[p]?.stock ?? 0);
  const price = partNames.map((p) => problem.parts[p]?.price ?? Number.POSITIVE_INFINITY);

  // 按“修复净收益”降序排列，加速限界。
  const devices = [...problem.devices].sort(
    (a, b) => b.repairedValue - b.residualValue - (a.repairedValue - a.residualValue),
  );
  const n = devices.length;

  const need: number[][] = [];
  const gain: number[] = [];
  const yieldParts: number[][] = [];
  const repairCost: number[] = [];
  const dismantleCost: number[] = [];
  const residual: number[] = [];
  let totalResidual = 0;

  for (const d of devices) {
    const rate = d.recoveryRate ?? globalRate;
    const needRow = new Array<number>(P).fill(0);
    for (const [part, qty] of Object.entries(d.needs)) needRow[partIndex.get(part)!] = qty;
    const yieldRow = new Array<number>(P).fill(0);
    for (const [part, qty] of Object.entries(d.contains)) {
      // 回收损耗：向下取整。
      yieldRow[partIndex.get(part)!] = Math.floor(qty * rate);
    }
    need.push(needRow);
    yieldParts.push(yieldRow);
    repairCost.push(d.repairCost);
    dismantleCost.push(d.dismantleCost ?? 0);
    residual.push(d.residualValue);
    gain.push(d.repairedValue - d.residualValue);
    totalResidual += d.residualValue;
  }

  // 后缀上界：剩余设备全部免费修复时的最大净收益（拆解的直接收益 <= 0，已含在内）。
  const suffixGain = new Array<number>(n + 1).fill(0);
  for (let i = n - 1; i >= 0; i--) {
    suffixGain[i] = suffixGain[i + 1] + Math.max(0, gain[i]);
  }
  // 后缀回收上界：剩余设备全部拆解时能回收的备件量（用于欠账可行性剪枝）。
  const suffixYield: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(P).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let p = 0; p < P; p++) suffixYield[i][p] = suffixYield[i + 1][p] + yieldParts[i][p];
  }

  const avail = new Int32Array(stock); // 库存 + 已回收 - 已消耗，允许为负（欠账）。
  const choices = new Uint8Array(n).fill(IDLE);
  let bestChoices = new Uint8Array(n).fill(IDLE);
  let bestValue = 0; // 净收益空间：Σ修复(修复价值-残值) - Σ拆解残值；全闲置为 0。
  let bestBudgetLeft = problem.budget;
  let nodes = 0;

  // 每层一张记忆表：(剩余预算, 当前备件向量) -> 已达成的最大净收益。
  const memo: Map<string, number>[] = Array.from({ length: n }, () => new Map());

  function dfs(i: number, budgetLeft: number, value: number): void {
    nodes++;
    if (value + suffixGain[i] <= bestValue) return;
    // 欠账可行性：即使剩余设备全部拆解，仍盖不住的缺口
    // 对不可外购备件 -> 不可行；对可外购备件 -> 外购花费下界，超预算则剪枝。
    let purchaseLowerBound = 0;
    for (let p = 0; p < P; p++) {
      const deficit = -(avail[p] + suffixYield[i][p]);
      if (deficit <= 0) continue;
      if (!Number.isFinite(price[p])) return;
      purchaseLowerBound += deficit * price[p];
    }
    if (purchaseLowerBound > budgetLeft) return;

    if (i === n) {
      // 期末统一结算：实际缺口外购。
      let purchaseCost = 0;
      for (let p = 0; p < P; p++) {
        if (avail[p] < 0) purchaseCost += -avail[p] * price[p];
      }
      if (purchaseCost > budgetLeft) return;
      bestValue = value;
      bestBudgetLeft = budgetLeft - purchaseCost;
      bestChoices = choices.slice();
      return;
    }

    const key = `${budgetLeft}|${avail.join(",")}`;
    const table = memo[i];
    const seen = table.get(key);
    if (seen !== undefined && seen >= value) return;
    table.set(key, value);

    // 分支 1：修复（备件记账，期末结算）。
    if (repairCost[i] <= budgetLeft) {
      for (let p = 0; p < P; p++) avail[p] -= need[i][p];
      choices[i] = REPAIR;
      dfs(i + 1, budgetLeft - repairCost[i], value + gain[i]);
      for (let p = 0; p < P; p++) avail[p] += need[i][p];
    }

    // 分支 2：拆解（损失残值，回收备件入库）。
    if (dismantleCost[i] <= budgetLeft) {
      for (let p = 0; p < P; p++) avail[p] += yieldParts[i][p];
      choices[i] = DISMANTLE;
      dfs(i + 1, budgetLeft - dismantleCost[i], value - residual[i]);
      for (let p = 0; p < P; p++) avail[p] -= yieldParts[i][p];
    }

    // 分支 3：闲置。
    choices[i] = IDLE;
    dfs(i + 1, budgetLeft, value);

    choices[i] = IDLE;
  }

  dfs(0, problem.budget, 0);

  const decisions: Record<string, Decision> = {};
  const names: Decision[] = ["repair", "dismantle", "idle"];
  for (let i = 0; i < n; i++) decisions[devices[i].id] = names[bestChoices[i]];

  // 外购总量：各备件 max(0, 修复总需求 - 库存 - 回收总量)。
  const purchases: Record<string, number> = {};
  for (let p = 0; p < P; p++) {
    let demand = 0;
    let supply = stock[p];
    for (let i = 0; i < n; i++) {
      if (bestChoices[i] === REPAIR) demand += need[i][p];
      else if (bestChoices[i] === DISMANTLE) supply += yieldParts[i][p];
    }
    const qty = Math.max(0, demand - supply);
    if (qty > 0) purchases[partNames[p]] = qty;
  }

  const { flows, leftoverRecovered } = buildFlows(problem, decisions, purchases);

  return {
    totalValue: bestValue + totalResidual,
    budgetUsed: problem.budget - bestBudgetLeft,
    decisions,
    purchases,
    flows,
    leftoverRecovered,
    stats: { nodes },
  };
}
