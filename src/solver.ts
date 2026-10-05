import type { Problem } from './types.ts';

export interface SolveOptions {
  /** 时间上限（毫秒），超时返回当前最优并标记 terminated */
  timeLimitMs?: number;
  /** 搜索节点上限，超限返回当前最优并标记 terminated */
  maxNodes?: number;
}

export interface SolveResult {
  /** optimal = 已证明最优；terminated = 达到时间/节点上限，返回当前最优 */
  status: 'optimal' | 'terminated';
  /** 目标值 = 恢复总价值 - 拆解残值损失 */
  objective: number;
  /** 修复设备恢复的总价值 */
  valueRestored: number;
  /** 拆解设备损失的残值合计 */
  residualLost: number;
  /** 已用预算（修复费用合计） */
  budgetUsed: number;
  repaired: string[];
  dismantled: string[];
  idle: string[];
  /** 实际探索的搜索节点数 */
  nodes: number;
}

interface Dev {
  id: string;
  cost: number;
  value: number;
  residual: number;
  /** 修复所需备件：(备件下标, 数量) */
  kit: Array<readonly [number, number]>;
  /** 拆解有效回收（已乘回收率并向下取整）：(备件下标, 数量) */
  salvage: Array<readonly [number, number]>;
}

const EPS = 1e-9;

class Terminated extends Error {}

/**
 * 精确求解：分支定界枚举每台设备 {修复, 拆解, 闲置} 三种互斥决策。
 *
 * 上界（松弛）：忽略备件约束，剩余设备按 修复价值/费用 密度做分数背包，
 * 拆解只带来非正收益故按 0 计 —— 是合法上界。
 *
 * 可行性剪枝：任一备件的已承诺需求 > 库存 + 已承诺回收 + 全部未决设备潜在回收
 * 时剪枝。回收是一次性的（修复与拆解互斥），不存在循环产料。
 */
export function solve(problem: Problem, options: SolveOptions = {}): SolveResult {
  validate(problem);

  const partIds = collectParts(problem);
  const partIndex = new Map(partIds.map((p, i) => [p, i] as const));
  const numParts = partIds.length;
  const yieldOf = (p: string): number => problem.partYield?.[p] ?? problem.recoveryYield;
  const stock = partIds.map((p) => problem.stock[p] ?? 0);

  // 有效回收量 = floor(数量 * 回收率)，回收损耗在此发生
  const effectiveSalvage = (parts: Record<string, number>): Array<readonly [number, number]> => {
    const out: Array<readonly [number, number]> = [];
    for (const [part, qty] of Object.entries(parts)) {
      const gained = Math.floor(qty * yieldOf(part) + EPS);
      if (gained > 0) out.push([partIndex.get(part)!, gained] as const);
    }
    return out;
  };

  const devs: Dev[] = problem.devices.map((d) => ({
    id: d.id,
    cost: d.repairCost,
    value: d.repairValue,
    residual: d.residualValue,
    kit: Object.entries(d.repairParts)
      .filter(([, q]) => q > 0)
      .map(([p, q]) => [partIndex.get(p)!, q] as const),
    salvage: effectiveSalvage(d.salvageParts),
  }));

  const n = devs.length;

  // 决策顺序：正修复价值设备按 价值/费用 密度降序在前，其余在后（配合上界计算）
  const density = (d: Dev): number => (d.cost <= 0 ? Number.POSITIVE_INFINITY : d.value / d.cost);
  const positives = devs.filter((d) => d.value > 0).sort((a, b) => density(b) - density(a));
  const others = devs.filter((d) => d.value <= 0);
  const order = [...positives, ...others];
  const numPos = positives.length;

  // 分数背包上界所需的前缀和（仅正价值设备，已按密度排序）
  const prefCost = new Float64Array(numPos + 1);
  const prefValue = new Float64Array(numPos + 1);
  for (let i = 0; i < numPos; i++) {
    prefCost[i + 1] = prefCost[i] + positives[i].cost;
    prefValue[i + 1] = prefValue[i] + positives[i].value;
  }

  /** 深度 d 起剩余设备在预算 B 内的价值上界（忽略备件约束的分数背包） */
  function upperBound(d: number, budgetLeft: number): number {
    if (d >= numPos || budgetLeft <= 0) return 0;
    // 二分：最大的 k，使 sum(cost[d..k)) <= B
    let lo = d;
    let hi = numPos;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (prefCost[mid] - prefCost[d] <= budgetLeft + EPS) lo = mid;
      else hi = mid - 1;
    }
    let bound = prefValue[lo] - prefValue[d];
    if (lo < numPos) {
      const remaining = budgetLeft - (prefCost[lo] - prefCost[d]);
      const nextCost = positives[lo].cost;
      if (nextCost > 0) bound += (remaining / nextCost) * positives[lo].value;
    }
    return bound;
  }

  // 全部设备回收总量（用于“修复永远不可行”预判）
  const totalSalvage = new Float64Array(numParts);
  for (const d of devs) for (const [p, q] of d.salvage) totalSalvage[p] += q;

  // 修复永远不可行：某备件 需求 > 库存 + 除自身外全部回收（不能自我供给）
  const repairPossible = order.map((d) => {
    if (d.cost > problem.budget + EPS) return false;
    const own = new Float64Array(numParts);
    for (const [p, q] of d.salvage) own[p] += q;
    return d.kit.every(([p, q]) => q <= stock[p] + totalSalvage[p] - own[p] + EPS);
  });

  // 拆解是否有意义：回收件中至少一种被某台设备的修复清单需要
  const usefulPart = new Array<boolean>(numParts).fill(false);
  for (const d of devs) for (const [p] of d.kit) usefulPart[p] = true;
  const dismantleUseful = order.map((d) => d.salvage.some(([p]) => usefulPart[p]));

  // suffixSalvage[d][p]：决策序列 [d..n) 内全部设备的潜在回收量（可行性剪枝用）
  const suffixSalvage: Float64Array[] = new Array(n + 1);
  suffixSalvage[n] = new Float64Array(numParts);
  for (let d = n - 1; d >= 0; d--) {
    const row = suffixSalvage[d] = suffixSalvage[d + 1].slice();
    for (const [p, q] of order[d].salvage) row[p] += q;
  }

  const choice = new Uint8Array(n); // 0 闲置 1 修复 2 拆解
  const bestChoice = new Uint8Array(n);
  const demand = new Float64Array(numParts); // 已承诺修复的备件需求
  const supplied = new Float64Array(numParts); // 已承诺拆解的备件回收
  const deadline = options.timeLimitMs != null ? Date.now() + options.timeLimitMs : Infinity;
  const maxNodes = options.maxNodes ?? Infinity;

  let nodes = 0;
  let best = -Infinity;

  function commit(): void {
    best = cur;
    bestChoice.set(choice);
  }

  let cur = 0;

  function dfs(d: number, budgetLeft: number): void {
    if (++nodes % 2048 === 0) {
      if (nodes > maxNodes || Date.now() > deadline) throw new Terminated();
    }
    // 可行性剪枝：需求超过 库存 + 已承诺回收 + 未决设备全部潜在回收
    for (let p = 0; p < numParts; p++) {
      if (demand[p] > stock[p] + supplied[p] + suffixSalvage[d][p] + EPS) return;
    }
    // 最优性剪枝：上界不超过当前最优
    if (cur + upperBound(d, budgetLeft) <= best + EPS) return;
    if (d === n) {
      commit();
      return;
    }
    const dev = order[d];
    // 分支 1：修复（预算够且理论上能齐套）
    if (repairPossible[d] && dev.cost <= budgetLeft + EPS) {
      choice[d] = 1;
      for (const [p, q] of dev.kit) demand[p] += q;
      cur += dev.value;
      dfs(d + 1, budgetLeft - dev.cost);
      cur -= dev.value;
      for (const [p, q] of dev.kit) demand[p] -= q;
    }
    // 分支 2：拆解（回收有用或残值为负时才可能优于闲置）
    if (dismantleUseful[d] || dev.residual < 0) {
      choice[d] = 2;
      for (const [p, q] of dev.salvage) supplied[p] += q;
      cur -= dev.residual;
      dfs(d + 1, budgetLeft);
      cur += dev.residual;
      for (const [p, q] of dev.salvage) supplied[p] -= q;
    }
    // 分支 3：闲置
    choice[d] = 0;
    dfs(d + 1, budgetLeft);
  }

  // 初始 incumbent：纯修复贪心（只用库存，按密度降序）
  {
    const avail = Float64Array.from(stock);
    let budgetLeft = problem.budget;
    let value = 0;
    const greedy = new Uint8Array(n);
    for (let d = 0; d < n; d++) {
      const dev = order[d];
      if (dev.value <= 0 || dev.cost > budgetLeft + EPS) continue;
      if (!dev.kit.every(([p, q]) => q <= avail[p] + EPS)) continue;
      greedy[d] = 1;
      budgetLeft -= dev.cost;
      value += dev.value;
      for (const [p, q] of dev.kit) avail[p] -= q;
    }
    best = value;
    bestChoice.set(greedy);
  }

  let status: SolveResult['status'] = 'optimal';
  try {
    dfs(0, problem.budget);
  } catch (e) {
    if (!(e instanceof Terminated)) throw e;
    status = 'terminated';
  }

  const repaired: string[] = [];
  const dismantled: string[] = [];
  const idle: string[] = [];
  let valueRestored = 0;
  let residualLost = 0;
  let budgetUsed = 0;
  for (let d = 0; d < n; d++) {
    const dev = order[d];
    if (bestChoice[d] === 1) {
      repaired.push(dev.id);
      valueRestored += dev.value;
      budgetUsed += dev.cost;
    } else if (bestChoice[d] === 2) {
      dismantled.push(dev.id);
      residualLost += dev.residual;
    } else {
      idle.push(dev.id);
    }
  }
  const byId = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);
  repaired.sort(byId);
  dismantled.sort(byId);
  idle.sort(byId);

  return {
    status,
    objective: valueRestored - residualLost,
    valueRestored,
    residualLost,
    budgetUsed,
    repaired,
    dismantled,
    idle,
    nodes,
  };
}

function collectParts(problem: Problem): string[] {
  const set = new Set<string>();
  for (const p of Object.keys(problem.stock)) set.add(p);
  for (const d of problem.devices) {
    for (const p of Object.keys(d.repairParts)) set.add(p);
    for (const p of Object.keys(d.salvageParts)) set.add(p);
  }
  return [...set].sort();
}

function validate(problem: Problem): void {
  if (!Number.isFinite(problem.budget) || problem.budget < 0) {
    throw new Error('预算必须是非负有限数');
  }
  if (problem.devices.length > 18) {
    throw new Error(`设备数量 ${problem.devices.length} 超过上限 18`);
  }
  const yields = problem.devices.flatMap((d) => Object.keys(d.salvageParts));
  for (const p of new Set(yields)) {
    const y = problem.partYield?.[p] ?? problem.recoveryYield;
    if (!(y > 0 && y <= 1)) throw new Error(`备件 ${p} 的回收率 ${y} 必须在 (0,1] 内`);
  }
  const ids = new Set<string>();
  for (const d of problem.devices) {
    if (ids.has(d.id)) throw new Error(`设备 ID 重复: ${d.id}`);
    ids.add(d.id);
    for (const [label, v] of [
      ['repairCost', d.repairCost],
      ['repairValue', d.repairValue],
      ['residualValue', d.residualValue],
    ] as const) {
      if (!Number.isFinite(v) || v < 0) throw new Error(`设备 ${d.id} 的 ${label} 非法: ${v}`);
    }
  }
}
