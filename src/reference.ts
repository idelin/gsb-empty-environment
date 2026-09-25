/**
 * 有限枚举参考检查器。
 * 独立于此求解器：直接对有限候选域做穷举枚举，
 * 验证求解器的可满足/不可满足判定与所给解的正确性。
 */
import type { DependentLimitObligation, LimitObligation, Obligation } from './obligations.ts';

export interface ReferenceCheckResult {
  solverVerdict: 'sat' | 'unsat';
  referenceVerdict: 'sat' | 'unsat';
  verdictsAgree: boolean;
  /** 枚举找到的全部满足赋值（有限域内） */
  satisfyingAssignments: Record<string, number>[];
  /** 求解器给出的解是否在枚举出的合法解之中 */
  solverSolutionValid: boolean;
}

function* enumerateAssignments(
  keys: string[],
  domains: number[][],
  i: number,
  acc: Record<string, number>,
): Generator<Record<string, number>> {
  if (i === keys.length) {
    yield { ...acc };
    return;
  }
  for (const v of domains[i]!) {
    acc[keys[i]!] = v;
    yield* enumerateAssignments(keys, domains, i + 1, acc);
  }
  delete acc[keys[i]!];
}

export function checkByFiniteEnumeration(
  obligations: Obligation[],
  solverSolution: Record<string, number> | null,
): ReferenceCheckResult {
  const limits = obligations.filter((o): o is LimitObligation => o.kind === 'limit');
  const deps = obligations.filter((o): o is DependentLimitObligation => o.kind === 'dependentLimit');

  const keys = [...new Set([
    ...limits.map((l) => l.key),
    ...deps.flatMap((d) => [d.key, d.relativeTo]),
  ])].sort();

  // 有限枚举域：义务中出现的全部取值及其按 minGap 的平移，外加边界邻域。
  const baseValues = new Set<number>(limits.map((l) => l.value));
  const gaps = deps.map((d) => d.minGap);
  const domain = new Set<number>();
  for (const v of baseValues) {
    domain.add(v);
    for (const g of gaps) {
      domain.add(v + g);
      domain.add(v - g);
    }
  }
  if (domain.size === 0) domain.add(0);
  const values = [...domain].sort((a, b) => a - b);

  const satisfies = (a: Record<string, number>): boolean => {
    for (const l of limits) {
      const v = a[l.key];
      if (v === undefined) return false;
      if (l.direction === 'max' && v > l.value) return false;
      if (l.direction === 'min' && v < l.value) return false;
    }
    for (const d of deps) {
      const kv = a[d.key];
      const rv = a[d.relativeTo];
      if (kv === undefined || rv === undefined) return false;
      if (kv < rv + d.minGap) return false;
    }
    return true;
  };

  const domains = keys.map(() => values);
  const satisfying: Record<string, number>[] = [];
  for (const assignment of enumerateAssignments(keys, domains, 0, {})) {
    if (satisfies(assignment)) satisfying.push(assignment);
  }

  const referenceVerdict = satisfying.length > 0 ? 'sat' : 'unsat';
  const solverVerdict = solverSolution !== null ? 'sat' : 'unsat';
  const solverSolutionValid =
    solverSolution === null
      ? referenceVerdict === 'unsat'
      : satisfies(solverSolution);

  return {
    solverVerdict,
    referenceVerdict,
    verdictsAgree: solverVerdict === referenceVerdict,
    satisfyingAssignments: satisfying,
    solverSolutionValid,
  };
}
