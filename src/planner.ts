import { Problem } from "./types";

/** 计划中的一步：动作、成本、本步新达成的目标、执行后的资源余额。 */
export interface PlanStep {
  action: string;
  cost: number;
  achievedNow: string[];
  balance: Record<string, number>;
}

/** 规划结果。 */
export interface PlanResult {
  profit: number;
  cost: number;
  steps: PlanStep[];
  achievedGoals: string[];
  finalBalance: Record<string, number>;
  statesExplored: number;
}

/** 被阻塞的目标：看似只差一个动作，实则关键资源已被耗尽。 */
export interface BlockedGoal {
  goal: string;
  lostProfit: number;
  /** 离该目标最近的那个动作（缺口最小）。 */
  closestAction: string;
  /** 关键资源缺口：need > have。 */
  criticalResources: { resource: string; need: number; have: number }[];
  /** 路径上消耗过关键资源的动作（“谁在半路吃掉了关键资源”）。 */
  consumedBy: { action: string; resource: string; amount: number }[];
}

/** 死端：可达、但未达成目标已无法全部继续推进的状态。 */
export interface DeadEnd {
  /** 到达该状态的动作序列（见证路径）。 */
  path: string[];
  balance: Record<string, number>;
  remainingGoals: string[];
  blockedGoals: BlockedGoal[];
}

interface DpEntry {
  profit: number;
  cost: number;
  length: number;
  nextAction: number; // -1 表示在此停止
}

interface StateNode {
  res: number[];
  mask: bigint;
  path: number[]; // 动作下标序列
}

/**
 * 资源受限规划器。
 *
 * 在有限状态空间（资源向量 × 目标位掩码）上做带记忆化的动态规划，
 * 保证全局最优：主目标最大化收益，次目标最小化动作成本，
 * 再次 tie-break 最少动作数。绝不按目标距离贪心。
 */
export class Planner {
  private readonly resourceNames: string[];
  private readonly goalNames: string[];
  private readonly goalProfit: number[];
  private readonly goalIndex = new Map<string, number>();
  private readonly consumeVec: number[][];
  private readonly achieveMask: bigint[];
  private readonly fullMask: bigint;
  private readonly memo = new Map<string, DpEntry>();
  private explored = 0;

  constructor(private readonly problem: Problem) {
    validateProblem(problem);
    this.resourceNames = problem.resources.map((r) => r.name);
    this.goalNames = problem.goals.map((g) => g.name);
    this.goalProfit = problem.goals.map((g) => g.profit);
    this.goalNames.forEach((name, i) => this.goalIndex.set(name, i));
    this.consumeVec = problem.actions.map((a) =>
      this.resourceNames.map((rn) => a.consumes[rn] ?? 0),
    );
    this.achieveMask = problem.actions.map((a) => {
      let m = 0n;
      for (const g of a.achieves) m |= 1n << BigInt(this.goalIndex.get(g)!);
      return m;
    });
    this.fullMask = (1n << BigInt(this.goalNames.length)) - 1n;
  }

  /** 求预算内收益最高（成本最低）的计划。 */
  plan(): PlanResult {
    const initial = this.problem.resources.map((r) => r.capacity);
    const entry = this.solve(initial, 0n);

    const steps: PlanStep[] = [];
    const achieved: string[] = [];
    let res = initial.slice();
    let mask = 0n;
    let cur: DpEntry | undefined = entry;
    while (cur && cur.nextAction >= 0) {
      const ai = cur.nextAction;
      const action = this.problem.actions[ai];
      res = res.map((v, i) => v - this.consumeVec[ai][i]);
      const gained = this.achieveMask[ai] & ~mask;
      mask |= this.achieveMask[ai];
      const achievedNow = this.maskToGoals(gained);
      achieved.push(...achievedNow);
      steps.push({
        action: action.name,
        cost: action.cost,
        achievedNow,
        balance: this.toRecord(res),
      });
      cur = this.memo.get(this.key(res, mask));
    }

    return {
      profit: entry.profit,
      cost: entry.cost,
      steps,
      achievedGoals: achieved,
      finalBalance: this.toRecord(res),
      statesExplored: this.explored,
    };
  }

  /**
   * 死端分析：枚举所有可达状态，找出“看似接近目标、
   * 实则关键资源已被消耗殆尽”的状态。
   */
  analyzeDeadEnds(maxStates = 100000): DeadEnd[] {
    const initial = this.problem.resources.map((r) => r.capacity);
    const seen = new Set<string>();
    const queue: StateNode[] = [{ res: initial, mask: 0n, path: [] }];
    seen.add(this.key(initial, 0n));
    const deadEnds: DeadEnd[] = [];

    while (queue.length > 0) {
      const node = queue.shift()!;
      const remaining = this.fullMask & ~node.mask;
      if (remaining !== 0n) {
        const best = this.solve(node.res, node.mask);
        if (best.profit === 0) {
          deadEnds.push(this.describeDeadEnd(node, remaining));
        }
      }
      if (seen.size >= maxStates) break;
      for (let ai = 0; ai < this.problem.actions.length; ai++) {
        if (!this.applicable(node.res, ai)) continue;
        const nres = node.res.map((v, i) => v - this.consumeVec[ai][i]);
        const nmask = node.mask | this.achieveMask[ai];
        const k = this.key(nres, nmask);
        if (seen.has(k)) continue;
        seen.add(k);
        queue.push({ res: nres, mask: nmask, path: [...node.path, ai] });
      }
    }

    const lost = (d: DeadEnd) =>
      d.blockedGoals.reduce((s, b) => s + b.lostProfit, 0);
    return deadEnds.sort((a, b) => lost(b) - lost(a));
  }

  private describeDeadEnd(node: StateNode, remaining: bigint): DeadEnd {
    const blockedGoals: BlockedGoal[] = [];
    for (const goalName of this.maskToGoals(remaining)) {
      const gi = this.goalIndex.get(goalName)!;
      // 找缺口最小的达成动作（“看似只差一步”的那个动作）。
      let bestAction = -1;
      let bestDeficit = Infinity;
      let bestCritical: { resource: string; need: number; have: number }[] = [];
      for (let ai = 0; ai < this.problem.actions.length; ai++) {
        if ((this.achieveMask[ai] & (1n << BigInt(gi))) === 0n) continue;
        const critical: { resource: string; need: number; have: number }[] = [];
        let deficit = 0;
        for (let ri = 0; ri < this.resourceNames.length; ri++) {
          const need = this.consumeVec[ai][ri];
          if (need > node.res[ri]) {
            deficit += need - node.res[ri];
            critical.push({
              resource: this.resourceNames[ri],
              need,
              have: node.res[ri],
            });
          }
        }
        if (deficit < bestDeficit) {
          bestDeficit = deficit;
          bestAction = ai;
          bestCritical = critical;
        }
      }
      // 回溯见证路径：谁消耗了这些关键资源。
      const consumedBy: { action: string; resource: string; amount: number }[] = [];
      for (const ai of node.path) {
        for (const c of bestCritical) {
          const ri = this.resourceNames.indexOf(c.resource);
          const amount = this.consumeVec[ai][ri];
          if (amount > 0) {
            consumedBy.push({
              action: this.problem.actions[ai].name,
              resource: c.resource,
              amount,
            });
          }
        }
      }
      blockedGoals.push({
        goal: goalName,
        lostProfit: this.goalProfit[gi],
        closestAction: this.problem.actions[bestAction].name,
        criticalResources: bestCritical,
        consumedBy,
      });
    }
    return {
      path: node.path.map((ai) => this.problem.actions[ai].name),
      balance: this.toRecord(node.res),
      remainingGoals: this.maskToGoals(remaining),
      blockedGoals,
    };
  }

  /** 记忆化 DP：返回从 (res, mask) 出发的最优后缀。 */
  private solve(res: number[], mask: bigint): DpEntry {
    const k = this.key(res, mask);
    const hit = this.memo.get(k);
    if (hit) return hit;
    this.explored++;

    let best: DpEntry = { profit: 0, cost: 0, length: 0, nextAction: -1 };
    for (let ai = 0; ai < this.problem.actions.length; ai++) {
      if (!this.applicable(res, ai)) continue;
      const gained = this.achieveMask[ai] & ~mask;
      // 跳过既不达成新目标也不改变状态的自环动作。
      if (gained === 0n && this.consumeVec[ai].every((c) => c === 0)) continue;
      const nres = res.map((v, i) => v - this.consumeVec[ai][i]);
      const sub = this.solve(nres, mask | this.achieveMask[ai]);
      const cand: DpEntry = {
        profit: this.maskProfit(gained) + sub.profit,
        cost: this.problem.actions[ai].cost + sub.cost,
        length: 1 + sub.length,
        nextAction: ai,
      };
      if (better(cand, best)) best = cand;
    }
    this.memo.set(k, best);
    return best;
  }

  private applicable(res: number[], ai: number): boolean {
    const cv = this.consumeVec[ai];
    for (let i = 0; i < res.length; i++) if (res[i] < cv[i]) return false;
    return true;
  }

  private maskProfit(mask: bigint): number {
    let total = 0;
    for (let i = 0; i < this.goalNames.length; i++) {
      if (mask & (1n << BigInt(i))) total += this.goalProfit[i];
    }
    return total;
  }

  private maskToGoals(mask: bigint): string[] {
    const out: string[] = [];
    for (let i = 0; i < this.goalNames.length; i++) {
      if (mask & (1n << BigInt(i))) out.push(this.goalNames[i]);
    }
    return out;
  }

  private key(res: number[], mask: bigint): string {
    return `${res.join(",")}#${mask.toString(36)}`;
  }

  private toRecord(res: number[]): Record<string, number> {
    const out: Record<string, number> = {};
    this.resourceNames.forEach((n, i) => (out[n] = res[i]));
    return out;
  }
}

/** 字典序比较：收益高者优先，其次成本低，再次动作数少。 */
function better(a: DpEntry, b: DpEntry): boolean {
  if (a.profit !== b.profit) return a.profit > b.profit;
  if (a.cost !== b.cost) return a.cost < b.cost;
  return a.length < b.length;
}

function validateProblem(p: Problem): void {
  const resourceNames = new Set(p.resources.map((r) => r.name));
  const goalNames = new Set(p.goals.map((g) => g.name));
  if (resourceNames.size !== p.resources.length)
    throw new Error("资源名重复");
  if (goalNames.size !== p.goals.length) throw new Error("目标名重复");
  for (const r of p.resources) {
    if (!Number.isInteger(r.capacity) || r.capacity < 0)
      throw new Error(`资源 ${r.name} 的 capacity 必须是非负整数`);
  }
  for (const g of p.goals) {
    if (!(g.profit >= 0)) throw new Error(`目标 ${g.name} 的 profit 不能为负`);
  }
  for (const a of p.actions) {
    if (!(a.cost >= 0)) throw new Error(`动作 ${a.name} 的 cost 不能为负`);
    for (const [rn, amount] of Object.entries(a.consumes)) {
      if (!resourceNames.has(rn))
        throw new Error(`动作 ${a.name} 消耗了未知资源 ${rn}`);
      if (!Number.isInteger(amount) || amount < 0)
        throw new Error(`动作 ${a.name} 对资源 ${rn} 的消耗量必须是非负整数`);
    }
    for (const g of a.achieves) {
      if (!goalNames.has(g)) throw new Error(`动作 ${a.name} 达成了未知目标 ${g}`);
    }
  }
}
