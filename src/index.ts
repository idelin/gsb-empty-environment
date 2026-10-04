import type { InstallPlan, SolveInput } from './types.ts';
import { Solver } from './solver.ts';
import { buildConflictReport } from './conflict.ts';
import type { ConflictReport } from './conflict.ts';

export type SolveResult =
  | { ok: true; plan: InstallPlan; upgrades: string[] }
  | { ok: false; conflict: ConflictReport };

/**
 * 求解安装方案。
 *
 * 有解时返回升级数最少（与 installed 不一致的包数量，含新装）的方案，
 * 并列时优先更高版本；`upgrades` 列出发生变化的包。
 * 无解时返回可复核的冲突子集（关于删除极小的不可满足约束集），
 * 可通过 `conflict.verify()` 复核，而不是只给出最后失败的依赖。
 */
export function solve(input: SolveInput): SolveResult {
  const solver = new Solver(input);
  const best = solver.optimize();
  if (best !== null) {
    const installed = input.installed ?? {};
    const upgrades = Object.keys(best.plan)
      .filter((k) => installed[k] !== best.plan[k])
      .sort();
    return { ok: true, plan: best.plan, upgrades };
  }
  return { ok: false, conflict: buildConflictReport(input) };
}

export * from './types.ts';
export { Solver, SearchLimitError } from './solver.ts';
export type { Best, SolveOptions } from './solver.ts';
export { buildConflictReport, describeConstraint, minimalUnsatisfiableSubset } from './conflict.ts';
export type { ConstraintItem, ConflictReport } from './conflict.ts';
export { inRange, intersectRange, formatRange } from './range.ts';
