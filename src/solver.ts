import { Employee, Problem, ShiftDef } from './types';
import { AbsInterval, gapBetween, overlaps, shiftInterval } from './time';

/** 可放松的硬规则标识（用于无解核心分析） */
export type RuleId = 'skill' | 'dailyHours' | 'rest' | 'unavailability';
export const ALL_RULES: RuleId[] = ['skill', 'dailyHours', 'rest', 'unavailability'];

export const RULE_DESCRIPTIONS: Record<RuleId, string> = {
  skill: '技能匹配（员工必须具备班次要求的技能）',
  dailyHours: '每日工时上限（员工当天工时不得超过 maxDailyMinutes）',
  rest: '跨日连续休息（相邻班次间隔不得少于 minRestMinutes）',
  unavailability: '员工不可用时段（班次不得与 unavailable 重叠）',
};

export interface SlotAssignment {
  shiftId: string;
  employeeId: string;
}

export type SolveStatus = 'optimal' | 'feasible' | 'infeasible' | 'unknown';

export interface OptimizeResult {
  status: SolveStatus;
  assignments: SlotAssignment[];
  /** 总偏好损失（infeasible/unknown 时为 Infinity） */
  cost: number;
  nodes: number;
}

export interface SolveOptions {
  /** 被临时禁用的规则（无解核心分析用） */
  disabledRules?: ReadonlySet<RuleId>;
  /** 只排这些班次，默认全部 */
  shifts?: ShiftDef[];
  /** 只判断是否可行，找到即停 */
  feasibilityOnly?: boolean;
  nodeBudget?: number;
}

interface Slot {
  shift: ShiftDef;
  interval: AbsInterval;
  duration: number;
  /** 同一班次（多人需求）展开后的前一个槽位下标，-1 表示组内第一个 */
  prevInGroup: number;
}

function preferenceCost(emp: Employee, shift: ShiftDef): number {
  let total = 0;
  for (const p of emp.preferences ?? []) {
    if (p.shiftId !== undefined && p.shiftId !== shift.id) continue;
    if (p.day !== undefined && p.day !== shift.day) continue;
    if (p.start !== undefined && p.end !== undefined) {
      const m = shift.start;
      const inWindow =
        p.end > p.start ? m >= p.start && m < p.end : m >= p.start || m < p.end;
      if (!inWindow) continue;
    }
    total += p.weight;
  }
  return total;
}

/**
 * 精确分支定界求解：
 *  - 班次槽位按 MRV（最少可行候选）动态选点；
 *  - 同一班次的多个名额按员工下标递增分配，消除排列对称；
 *  - 下界 = 已产生损失 + 每个未分配槽位的最小可能损失之和。
 */
export function solveProblem(problem: Problem, options: SolveOptions = {}): OptimizeResult {
  const disabled = options.disabledRules ?? new Set<RuleId>();
  const shifts = options.shifts ?? problem.shifts;
  const feasibilityOnly = options.feasibilityOnly ?? false;
  const nodeBudget = options.nodeBudget ?? (feasibilityOnly ? 200000 : 2000000);

  const employees = problem.employees;
  const E = employees.length;

  const slots: Slot[] = [];
  for (const shift of shifts) {
    const interval = shiftInterval(shift.day, shift.start, shift.end);
    const required = shift.required ?? 1;
    for (let k = 0; k < required; k++) {
      slots.push({
        shift,
        interval,
        duration: interval.end - interval.start,
        prevInGroup: k === 0 ? -1 : slots.length - 1,
      });
    }
  }
  const S = slots.length;

  const skillOn = !disabled.has('skill');
  const unavailOn = !disabled.has('unavailability');
  const hoursOn = !disabled.has('dailyHours');
  const restOn = !disabled.has('rest');
  const minRest = problem.minRestMinutes;

  const absUnavail: AbsInterval[][] = employees.map((e) =>
    (e.unavailable ?? []).map((w) => shiftInterval(w.day, w.start, w.end)),
  );

  const candidates: number[][] = slots.map((slot) => {
    const list: number[] = [];
    for (let e = 0; e < E; e++) {
      if (skillOn && !employees[e].skills.includes(slot.shift.skill)) continue;
      if (unavailOn && absUnavail[e].some((w) => overlaps(w, slot.interval))) continue;
      list.push(e);
    }
    return list;
  });

  const prefCost: number[][] = slots.map((slot) =>
    employees.map((emp) => preferenceCost(emp, slot.shift)),
  );

  const slotMin: number[] = slots.map((_, s) => {
    let m = Infinity;
    for (const e of candidates[s]) m = Math.min(m, prefCost[s][e]);
    return m === Infinity ? 0 : m;
  });

  const assign = new Array<number>(S).fill(-1);
  const empSlots: number[][] = employees.map(() => []);
  const dayMinutes: number[][] = employees.map(() => new Array<number>(7).fill(0));

  let remaining = 0;
  for (const m of slotMin) remaining += m;

  let nodes = 0;
  let withinBudget = true;
  let foundAny = false;
  let bestCost = Infinity;
  let bestAssign: number[] | null = null;
  let pendingList: number[] = [];

  function canAssign(s: number, e: number): boolean {
    const slot = slots[s];
    if (hoursOn && dayMinutes[e][slot.shift.day] + slot.duration > employees[e].maxDailyMinutes) {
      return false;
    }
    const iv = slot.interval;
    for (const t of empSlots[e]) {
      const o = slots[t].interval;
      if (overlaps(iv, o)) return false; // 时间重叠永远是硬约束
      if (restOn && gapBetween(iv, o) < minRest) return false;
    }
    return true;
  }

  function feasibleCandidates(s: number): number[] {
    const slot = slots[s];
    let list = candidates[s];
    if (slot.prevInGroup >= 0) {
      const prevEmp = assign[slot.prevInGroup];
      list = list.filter((e) => e > prevEmp);
    }
    return list.filter((e) => canAssign(s, e));
  }

  function pickSlot(): number {
    let best = -1;
    let bestCount = Infinity;
    pendingList = [];
    for (let s = 0; s < S; s++) {
      if (assign[s] !== -1) continue;
      const prev = slots[s].prevInGroup;
      if (prev >= 0 && assign[prev] === -1) continue; // 组内按顺序分配
      const list = feasibleCandidates(s);
      if (list.length === 0) {
        pendingList = list;
        return s;
      }
      if (list.length < bestCount) {
        best = s;
        bestCount = list.length;
        pendingList = list;
      }
    }
    return best;
  }

  function dfs(cost: number): void {
    if (!withinBudget) return;
    if (feasibilityOnly && foundAny) return;
    if (nodes++ >= nodeBudget) {
      withinBudget = false;
      return;
    }
    if (cost + remaining >= bestCost) return;

    const s = pickSlot();
    if (s === -1) {
      foundAny = true;
      if (cost < bestCost) {
        bestCost = cost;
        bestAssign = assign.slice();
      }
      return;
    }
    const list = pendingList;
    if (list.length === 0) return;

    const slot = slots[s];
    remaining -= slotMin[s];
    const ordered = list.slice().sort((a, b) => prefCost[s][a] - prefCost[s][b]);
    for (const e of ordered) {
      assign[s] = e;
      empSlots[e].push(s);
      dayMinutes[e][slot.shift.day] += slot.duration;
      dfs(cost + prefCost[s][e]);
      dayMinutes[e][slot.shift.day] -= slot.duration;
      empSlots[e].pop();
      assign[s] = -1;
      if (!withinBudget || (feasibilityOnly && foundAny)) break;
    }
    remaining += slotMin[s];
  }

  dfs(0);

  const assignments: SlotAssignment[] = [];
  if (bestAssign) {
    for (let s = 0; s < S; s++) {
      assignments.push({ shiftId: slots[s].shift.id, employeeId: employees[bestAssign[s]].id });
    }
  }

  let status: SolveStatus;
  if (foundAny) {
    status = feasibilityOnly ? 'feasible' : withinBudget ? 'optimal' : 'feasible';
  } else {
    status = withinBudget ? 'infeasible' : 'unknown';
  }

  return { status, assignments, cost: foundAny ? bestCost : Infinity, nodes };
}
