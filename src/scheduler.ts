import { Problem } from './types';
import { gapBetween, overlaps, shiftInterval } from './time';
import { SlotAssignment, solveProblem, SolveStatus } from './solver';
import { findMinimalInfeasibleCore, InfeasibleCore } from './core';

export interface ValidationError {
  message: string;
}

export function validateProblem(problem: Problem): ValidationError[] {
  const errors: ValidationError[] = [];
  if (problem.employees.length === 0) errors.push({ message: '至少需要 1 名员工' });
  if (problem.employees.length > 12) errors.push({ message: '员工数量不能超过 12' });
  if (problem.minRestMinutes < 0) errors.push({ message: 'minRestMinutes 不能为负' });

  const inRange = (m: number) => Number.isInteger(m) && m >= 0 && m < 1440;
  for (const s of problem.shifts) {
    if (!Number.isInteger(s.day) || s.day < 0 || s.day > 6) {
      errors.push({ message: `班次 ${s.id}：day 必须是 0-6` });
    }
    if (!inRange(s.start) || !inRange(s.end)) {
      errors.push({ message: `班次 ${s.id}：start/end 必须是 0-1439 的整数` });
    }
    if (!s.skill) errors.push({ message: `班次 ${s.id}：缺少 skill` });
    if (s.required !== undefined && (!Number.isInteger(s.required) || s.required < 1)) {
      errors.push({ message: `班次 ${s.id}：required 必须是正整数` });
    }
  }
  const ids = new Set<string>();
  for (const e of problem.employees) {
    if (ids.has(e.id)) errors.push({ message: `员工 id 重复：${e.id}` });
    ids.add(e.id);
    if (e.maxDailyMinutes <= 0) {
      errors.push({ message: `员工 ${e.id}：maxDailyMinutes 必须为正` });
    }
  }
  return errors;
}

export interface ScheduleEntry {
  shiftId: string;
  employeeId: string;
  day: number;
  start: number;
  end: number;
  preferenceCost: number;
}

export interface EmployeeStats {
  employeeId: string;
  shiftCount: number;
  totalMinutes: number;
  dailyMinutes: number[];
  preferenceCost: number;
}

export interface ScheduleResult {
  status: SolveStatus | 'invalid';
  errors?: ValidationError[];
  schedule: ScheduleEntry[];
  /** 总偏好损失 */
  totalPreferenceCost: number;
  /** 是否已证明最优（预算内搜索完毕） */
  optimal: boolean;
  stats: EmployeeStats[];
  /** 无解时的最小班次与规则集合 */
  core?: InfeasibleCore;
  nodes: number;
}

function entryPreferenceCost(
  emp: { preferences?: { shiftId?: string; day?: number; start?: number; end?: number; weight: number }[] },
  shift: { id: string; day: number; start: number },
): number {
  let cost = 0;
  for (const p of emp.preferences ?? []) {
    if (p.shiftId !== undefined && p.shiftId !== shift.id) continue;
    if (p.day !== undefined && p.day !== shift.day) continue;
    if (p.start !== undefined && p.end !== undefined) {
      const m = shift.start;
      const inWindow =
        p.end > p.start ? m >= p.start && m < p.end : m >= p.start || m < p.end;
      if (!inWindow) continue;
    }
    cost += p.weight;
  }
  return cost;
}

export function schedule(problem: Problem, nodeBudget?: number): ScheduleResult {
  const errors = validateProblem(problem);
  if (errors.length > 0) {
    return {
      status: 'invalid',
      errors,
      schedule: [],
      totalPreferenceCost: Infinity,
      optimal: false,
      stats: [],
      nodes: 0,
    };
  }

  const result = solveProblem(problem, { nodeBudget });

  const shiftById = new Map(problem.shifts.map((s) => [s.id, s]));
  const empById = new Map(problem.employees.map((e) => [e.id, e]));
  const entries: ScheduleEntry[] = result.assignments.map((a: SlotAssignment) => {
    const shift = shiftById.get(a.shiftId)!;
    const emp = empById.get(a.employeeId)!;
    return {
      shiftId: a.shiftId,
      employeeId: a.employeeId,
      day: shift.day,
      start: shift.start,
      end: shift.end,
      preferenceCost: entryPreferenceCost(emp, shift),
    };
  });
  entries.sort((a, b) => a.day - b.day || a.start - b.start || a.shiftId.localeCompare(b.shiftId));

  const stats: EmployeeStats[] = problem.employees.map((emp) => {
    const mine = entries.filter((e) => e.employeeId === emp.id);
    const daily = new Array<number>(7).fill(0);
    let total = 0;
    let pref = 0;
    for (const e of mine) {
      const iv = shiftInterval(e.day, e.start, e.end);
      const dur = iv.end - iv.start;
      daily[e.day] += dur;
      total += dur;
      pref += e.preferenceCost;
    }
    return {
      employeeId: emp.id,
      shiftCount: mine.length,
      totalMinutes: total,
      dailyMinutes: daily,
      preferenceCost: pref,
    };
  });

  const out: ScheduleResult = {
    status: result.status,
    schedule: entries,
    totalPreferenceCost: result.cost,
    optimal: result.status === 'optimal',
    stats,
    nodes: result.nodes,
  };

  if (result.status === 'infeasible') {
    out.core = findMinimalInfeasibleCore(problem) ?? undefined;
  }
  return out;
}

/** 独立校验器：检查一份排班结果是否满足全部硬约束（供测试与调用方复核） */
export function verify(problem: Problem, assignments: SlotAssignment[]): string[] {
  const violations: string[] = [];
  const shiftById = new Map(problem.shifts.map((s) => [s.id, s]));
  const empById = new Map(problem.employees.map((e) => [e.id, e]));

  const need = new Map<string, number>();
  for (const s of problem.shifts) need.set(s.id, s.required ?? 1);
  const got = new Map<string, number>();
  for (const a of assignments) got.set(a.shiftId, (got.get(a.shiftId) ?? 0) + 1);
  for (const [id, n] of need) {
    if ((got.get(id) ?? 0) !== n) violations.push(`班次 ${id} 覆盖人数不符`);
  }

  const perEmp = new Map<string, { start: number; end: number; day: number; dur: number }[]>();
  for (const a of assignments) {
    const shift = shiftById.get(a.shiftId);
    const emp = empById.get(a.employeeId);
    if (!shift || !emp) {
      violations.push(`未知班次或员工：${a.shiftId}/${a.employeeId}`);
      continue;
    }
    if (!emp.skills.includes(shift.skill)) {
      violations.push(`员工 ${emp.id} 不具备班次 ${shift.id} 所需技能 ${shift.skill}`);
    }
    const iv = shiftInterval(shift.day, shift.start, shift.end);
    for (const w of emp.unavailable ?? []) {
      if (overlaps(iv, shiftInterval(w.day, w.start, w.end))) {
        violations.push(`员工 ${emp.id} 班次 ${shift.id} 与不可用时段重叠`);
      }
    }
    const list = perEmp.get(emp.id) ?? [];
    list.push({ start: iv.start, end: iv.end, day: shift.day, dur: iv.end - iv.start });
    perEmp.set(emp.id, list);
  }

  for (const [empId, list] of perEmp) {
    const emp = empById.get(empId)!;
    const daily = new Array<number>(7).fill(0);
    for (const it of list) daily[it.day] += it.dur;
    for (let d = 0; d < 7; d++) {
      if (daily[d] > emp.maxDailyMinutes) {
        violations.push(`员工 ${empId} 第 ${d} 天工时 ${daily[d]} 超过上限 ${emp.maxDailyMinutes}`);
      }
    }
    const sorted = list.slice().sort((a, b) => a.start - b.start);
    for (let i = 0; i + 1 < sorted.length; i++) {
      if (overlaps(sorted[i], sorted[i + 1])) {
        violations.push(`员工 ${empId} 存在时间重叠的班次`);
      } else if (gapBetween(sorted[i], sorted[i + 1]) < problem.minRestMinutes) {
        violations.push(`员工 ${empId} 相邻班次休息不足 ${problem.minRestMinutes} 分钟`);
      }
    }
  }
  return violations;
}
