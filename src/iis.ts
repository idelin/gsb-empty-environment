import { ANY_SKILL, buildModel } from './model.ts';
import { solve } from './solver.ts';
import type { InfeasibilityExplanation, ScheduleInput, SolveOptions } from './types.ts';
import { DAYS, MINUTES_PER_DAY } from './types.ts';
import { hhmm } from './util.ts';

interface Element {
  key: string;
  kind: 'shift' | 'rule';
  describe: string;
}

/**
 * 删除法（deletion filter）求最小不可解集合：
 * 逐个尝试移除「班次 / 规则」元素，若移除后仍然不可解则永久移除；
 * 最终剩下的集合是不可约的——再去掉任意一项即恢复可行。
 * （是最小/minimal 不可约集合，不保证全局基数最小。）
 */
export function explainInfeasible(input: ScheduleInput, opts: SolveOptions = {}): InfeasibilityExplanation {
  const elements: Element[] = [];
  for (const s of input.shifts) {
    elements.push({
      key: `shift:${s.id}`,
      kind: 'shift',
      describe: `班次 ${s.id}（第 ${s.day + 1} 天 ${hhmm(s.startMinute)}-${hhmm(s.endMinute)}，技能 ${s.skill}，需 ${s.required ?? 1} 人）`,
    });
    elements.push({
      key: `skill:${s.id}`,
      kind: 'rule',
      describe: `规则：班次 ${s.id} 需要技能「${s.skill}」`,
    });
  }
  elements.push({
    key: 'rest',
    kind: 'rule',
    describe: `规则：同一员工相邻班次间连续休息 ≥ ${input.minRestMinutes} 分钟`,
  });
  for (const e of input.employees) {
    elements.push({
      key: `daily:${e.id}`,
      kind: 'rule',
      describe: `规则：员工 ${e.id} 每日工时 ≤ ${e.maxDailyMinutes} 分钟`,
    });
    (e.unavailable ?? []).forEach((w, i) => {
      elements.push({
        key: `unavailable:${e.id}:${i}`,
        kind: 'rule',
        describe: `规则：员工 ${e.id} 第 ${w.day + 1} 天 ${hhmm(w.startMinute)}-${hhmm(w.endMinute)} 不可用`,
      });
    });
  }

  const removed = new Set<string>();
  const stillInfeasible = (): boolean => {
    const relaxed = applyRelaxations(input, removed);
    const model = buildModel(relaxed);
    const out = solve(model, {
      optimize: false,
      nodeBudget: opts.nodeBudget ?? 200_000,
      timeBudgetMs: opts.timeBudgetMs ?? 5_000,
    });
    // unknown（预算耗尽）按可行处理：保守地保留该元素，不误删。
    return out.status === 'infeasible';
  };

  for (const el of elements) {
    removed.add(el.key);
    if (!stillInfeasible()) removed.delete(el.key);
  }

  const kept = elements.filter((el) => !removed.has(el.key));
  const shifts = kept.filter((el) => el.kind === 'shift').map((el) => el.describe);
  const rules = kept.filter((el) => el.kind === 'rule').map((el) => el.describe);
  const summary =
    `最小不可解集合：${shifts.length} 个班次 + ${rules.length} 条规则。` +
    '移除其中任意一项即可恢复可行；请扩充合格人手、放宽对应规则或削减班次需求。';
  return { shifts, rules, summary };
}

/** 按移除集合放松输入：删班次、通配技能、清零休息要求、放开每日工时、移除不可用窗口。 */
export function applyRelaxations(input: ScheduleInput, removed: ReadonlySet<string>): ScheduleInput {
  const shifts = input.shifts
    .filter((s) => !removed.has(`shift:${s.id}`))
    .map((s) => (removed.has(`skill:${s.id}`) ? { ...s, skill: ANY_SKILL } : s));
  const employees = input.employees.map((e) => ({
    ...e,
    maxDailyMinutes: removed.has(`daily:${e.id}`) ? DAYS * MINUTES_PER_DAY : e.maxDailyMinutes,
    unavailable: (e.unavailable ?? []).filter((_, i) => !removed.has(`unavailable:${e.id}:${i}`)),
  }));
  return {
    employees,
    shifts,
    minRestMinutes: removed.has('rest') ? 0 : input.minRestMinutes,
    preferences: [], // 可行性判定与偏好无关
  };
}
