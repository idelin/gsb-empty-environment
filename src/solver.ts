import type { Model } from './model.ts';

export interface SolveParams {
  optimize: boolean; // false = 仅判定可行性（用于不可解解释）
  nodeBudget: number;
  timeBudgetMs: number;
}

export interface SolveOutcome {
  status: 'solved' | 'infeasible' | 'unknown';
  provenOptimal: boolean;
  assignment?: number[][]; // 每个班次的员工下标列表
  cost?: number; // 偏好总损失（optimize 模式）
  nodes: number;
}

class BudgetExceeded extends Error {}

const ALL_DONE = -1;
const CONFLICT = -2;

/**
 * 约束传播 + 分支限界：
 *  - 变量：每个班次的每个名额；同班次名额无序（按组合枚举，天然破对称）。
 *  - 硬约束：技能/不可用（预过滤）、每日工时、跨日最小连续休息。
 *  - 前向检查：每个未排满班次的可用候选人数必须 >= 剩余名额。
 *  - 目标：avoid 偏好损失在分配时累计；prefer 损失在叶子结算，
 *    下界 = 已发生损失 + 已不可能满足的 prefer 损失。
 */
export function solve(model: Model, params: SolveParams): SolveOutcome {
  const { shifts, employees, prefs, minRest, candidate } = model;
  const S = shifts.length;
  const E = employees.length;
  const assigned: number[][] = shifts.map(() => []);
  const empShifts: number[][] = employees.map(() => []);
  const dayMin: number[][] = employees.map(() => new Array<number>(7).fill(0));
  let incurred = 0;
  let best = Infinity;
  let bestAssignment: number[][] | undefined;
  let found = false;
  let nodes = 0;
  const deadline = Date.now() + params.timeBudgetMs;

  const avoidDelta: number[][] = employees.map((_, e) =>
    shifts.map((_, s) => {
      let c = 0;
      for (const p of prefs) if (p.type === 'avoid' && p.emp === e && p.shifts[s]) c += p.weight;
      return c;
    }),
  );

  const restOk = (e: number, s: number): boolean => {
    const sh = shifts[s]!;
    for (const t of empShifts[e]!) {
      const th = shifts[t]!;
      if (!(th.end + minRest <= sh.start || sh.end + minRest <= th.start)) return false;
    }
    return true;
  };

  const canAssign = (e: number, s: number): boolean =>
    candidate[e]![s]! &&
    !assigned[s]!.includes(e) &&
    dayMin[e]![shifts[s]!.day]! + shifts[s]!.duration <= employees[e]!.maxDailyMinutes &&
    restOk(e, s);

  // 选择下一个要排的班次：剩余名额缺口最小（MRV）；任一班次候选不足则冲突。
  const pickShift = (): number => {
    let chosen = ALL_DONE;
    let bestSlack = Infinity;
    for (let s = 0; s < S; s++) {
      const need = shifts[s]!.required - assigned[s]!.length;
      if (need <= 0) continue;
      let count = 0;
      for (let e = 0; e < E; e++) if (canAssign(e, s)) count++;
      if (count < need) return CONFLICT;
      const slack = count - need;
      if (slack < bestSlack) {
        bestSlack = slack;
        chosen = s;
        if (slack === 0) break;
      }
    }
    return chosen;
  };

  // prefer 损失：onlyUnavoidable=true 时只统计目标班次已全部排满、确定无法满足的部分（作为下界）。
  const preferLoss = (onlyUnavoidable: boolean): number => {
    let loss = 0;
    for (const p of prefs) {
      if (p.type !== 'prefer') continue;
      let satisfied = false;
      let allFull = true;
      for (let s = 0; s < S; s++) {
        if (!p.shifts[s]) continue;
        if (assigned[s]!.includes(p.emp)) {
          satisfied = true;
          break;
        }
        if (assigned[s]!.length < shifts[s]!.required) allFull = false;
      }
      if (!satisfied && (!onlyUnavoidable || allFull)) loss += p.weight;
    }
    return loss;
  };

  const combosOf = (cands: number[], k: number): number[][] => {
    const out: number[][] = [];
    const acc: number[] = [];
    const rec = (i: number): void => {
      if (acc.length === k) {
        out.push(acc.slice());
        return;
      }
      if (cands.length - i < k - acc.length) return;
      for (let j = i; j < cands.length; j++) {
        acc.push(cands[j]!);
        rec(j + 1);
        acc.pop();
      }
    };
    rec(0);
    return out;
  };

  const recurse = (): void => {
    if (found && !params.optimize) return;
    nodes++;
    if ((nodes & 1023) === 0 && (nodes > params.nodeBudget || Date.now() > deadline)) {
      throw new BudgetExceeded();
    }
    const s = pickShift();
    if (s === CONFLICT) return;
    if (s === ALL_DONE) {
      const total = params.optimize ? incurred + preferLoss(false) : 0;
      if (total < best) {
        best = total;
        bestAssignment = assigned.map((a) => a.slice());
      }
      found = true;
      return;
    }
    if (params.optimize && incurred + preferLoss(true) >= best) return;

    const sh = shifts[s]!;
    const need = sh.required - assigned[s]!.length;
    const cands: number[] = [];
    for (let e = 0; e < E; e++) if (canAssign(e, s)) cands.push(e);
    const combos = combosOf(cands, need);
    combos.sort((a, b) => {
      const cost = (combo: number[]): number => combo.reduce((acc2, e) => acc2 + avoidDelta[e]![s]!, 0);
      return cost(a) - cost(b);
    });
    for (const combo of combos) {
      let delta = 0;
      for (const e of combo) {
        assigned[s]!.push(e);
        empShifts[e]!.push(s);
        dayMin[e]![sh.day]! += sh.duration;
        delta += avoidDelta[e]![s]!;
      }
      incurred += delta;
      recurse();
      incurred -= delta;
      for (const e of combo) {
        assigned[s]!.pop();
        empShifts[e]!.pop();
        dayMin[e]![sh.day]! -= sh.duration;
      }
      if (found && !params.optimize) return;
    }
  };

  try {
    recurse();
  } catch (err) {
    if (!(err instanceof BudgetExceeded)) throw err;
    if (found) {
      return { status: 'solved', provenOptimal: false, assignment: bestAssignment, cost: best, nodes };
    }
    return { status: 'unknown', provenOptimal: false, nodes };
  }
  if (!found) return { status: 'infeasible', provenOptimal: false, nodes };
  return { status: 'solved', provenOptimal: true, assignment: bestAssignment, cost: best, nodes };
}
