import { Problem, ShiftDef } from './types';
import { ALL_RULES, RuleId, solveProblem } from './solver';

/** 导致无解的最小（删除极小）班次与规则集合 */
export interface InfeasibleCore {
  /** 核心班次：移除其中任意一个，问题恢复可解 */
  shifts: ShiftDef[];
  /** 核心规则：放宽其中任意一条，问题恢复可解 */
  rules: RuleId[];
  /** 涉及的员工（具备核心班次所需技能的员工） */
  employeeIds: string[];
}

/**
 * 删除式最小化（deletion-based MUS 提取）：
 * 1. 逐个尝试移除班次，若移除后仍不可解则永久移除；
 * 2. 逐个尝试禁用规则，若禁用后仍不可解则永久禁用。
 * 结果保证：剩余集合不可解，且再移除任一班次 / 放宽任一规则即可解。
 * 若判定过程超出搜索预算，出于保守会保留该元素（不会误报更小的核心）。
 */
export function findMinimalInfeasibleCore(
  problem: Problem,
  nodeBudget = 200000,
): InfeasibleCore | null {
  const isInfeasible = (shifts: ShiftDef[], disabled: ReadonlySet<RuleId>): boolean =>
    solveProblem(problem, { shifts, disabledRules: disabled, feasibilityOnly: true, nodeBudget })
      .status === 'infeasible';

  if (!isInfeasible(problem.shifts, new Set<RuleId>())) return null;

  let shifts = problem.shifts.slice();
  for (const shift of problem.shifts) {
    const trial = shifts.filter((s) => s !== shift);
    if (trial.length === shifts.length) continue;
    if (isInfeasible(trial, new Set<RuleId>())) shifts = trial;
  }

  const disabled = new Set<RuleId>();
  for (const rule of ALL_RULES) {
    const trial = new Set<RuleId>(disabled);
    trial.add(rule);
    if (isInfeasible(shifts, trial)) {
      disabled.add(rule);
    }
  }

  const rules = ALL_RULES.filter((r) => !disabled.has(r));
  const skills = new Set(shifts.map((s) => s.skill));
  const employeeIds = problem.employees
    .filter((e) => e.skills.some((k) => skills.has(k)))
    .map((e) => e.id);

  return { shifts, rules, employeeIds };
}
