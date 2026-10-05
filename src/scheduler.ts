import { buildModel } from './model.ts';
import { solve } from './solver.ts';
import { explainInfeasible } from './iis.ts';
import type {
  PreferenceLossEntry,
  ScheduleInput,
  ScheduleResult,
  SolveOptions,
} from './types.ts';

/**
 * 求解七天排班：
 *  - 满足全部硬约束（技能、每日工时、跨日连续休息、不可用时间、人数需求）；
 *  - 在可行解中使带权偏好损失最小；
 *  - 不可解时返回导致无解的最小班次与规则集合。
 */
export function schedule(input: ScheduleInput, opts: SolveOptions = {}): ScheduleResult {
  const model = buildModel(input);
  const out = solve(model, {
    optimize: true,
    nodeBudget: opts.nodeBudget ?? 2_000_000,
    timeBudgetMs: opts.timeBudgetMs ?? 10_000,
  });

  if (out.status === 'infeasible') {
    return { status: 'infeasible', explanation: explainInfeasible(input, opts) };
  }
  const assignment = out.assignment;
  if (out.status === 'unknown' || assignment === undefined) {
    return {
      status: 'unknown',
      reason: '超出搜索预算：既未找到可行解，也无法证明不可解。可调大 nodeBudget / timeBudgetMs 后重试。',
    };
  }

  const assignments = model.shifts.map((sh, s) => ({
    shiftId: sh.id,
    employeeIds: assignment[s]!.map((e) => model.employees[e]!.id),
  }));

  const losses: PreferenceLossEntry[] = [];
  for (const p of model.prefs) {
    const hit = model.shifts.some((_, s) => p.shifts[s] && assignment[s]!.includes(p.emp));
    const lost = p.type === 'prefer' ? !hit : hit;
    if (lost) {
      losses.push({ employee: model.employees[p.emp]!.id, type: p.type, weight: p.weight, target: p.target });
    }
  }

  return {
    status: out.provenOptimal ? 'optimal' : 'feasible',
    assignments,
    totalLoss: out.cost ?? 0,
    losses,
    stats: { nodes: out.nodes },
  };
}
