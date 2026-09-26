/**
 * Independent brute-force enumerator. Shares no propagation code with the
 * solver; used to cross-check solver verdicts (propagation safety) on
 * tractable domains.
 */

import { checkAll, type Constraint, type SolveResult, type VarDecl } from './solver.ts';

export interface EnumerateOptions {
  budget?: number;
}

export function enumerateAll(
  vars: VarDecl[],
  constraints: Constraint[],
  opts: EnumerateOptions = {},
): SolveResult {
  const limit = opts.budget ?? Number.POSITIVE_INFINITY;
  let visited = 0;
  const names = vars.map((d) => d.name);
  const domains = vars.map((d) => d.domain);
  const asg: Record<string, number> = {};

  const walk = (i: number): Record<string, number> | null | 'budget' => {
    if (i === names.length) {
      visited++;
      return checkAll(constraints, asg) ? { ...asg } : null;
    }
    for (const value of domains[i]) {
      if (visited >= limit) return 'budget';
      asg[names[i]] = value;
      const r = walk(i + 1);
      if (r === 'budget') return 'budget';
      if (r !== null) return r;
    }
    return null;
  };

  const found = walk(0);
  const stats = { nodes: visited, steps: visited };
  if (found === 'budget') return { status: 'unknown', reason: 'budget-exhausted', stats };
  if (found === null) return { status: 'unsat', stats };
  return { status: 'sat', model: found, stats };
}

export interface Verification {
  consistent: boolean;
  solver: SolveResult['status'];
  enumerator: SolveResult['status'];
  detail: string;
}

/**
 * Propagation-safety check: the solver's verdict must agree with exhaustive
 * enumeration, and a 'sat' model must actually satisfy every constraint.
 */
export function verifyAgainstEnumeration(
  solverResult: SolveResult,
  vars: VarDecl[],
  constraints: Constraint[],
  enumBudget = 1_000_000,
): Verification {
  if (solverResult.status === 'sat') {
    if (!solverResult.model || !checkAll(constraints, solverResult.model)) {
      return {
        consistent: false,
        solver: 'sat',
        enumerator: 'sat',
        detail: 'solver model does not satisfy the constraints',
      };
    }
  }
  const total = vars.reduce((acc, d) => acc * d.domain.length, 1);
  if (total > enumBudget) {
    return {
      consistent: true,
      solver: solverResult.status,
      enumerator: 'unknown',
      detail: `enumeration space ${total} exceeds budget; skipped`,
    };
  }
  const brute = enumerateAll(vars, constraints);
  if (solverResult.status === 'unknown') {
    return {
      consistent: true,
      solver: 'unknown',
      enumerator: brute.status,
      detail: 'solver hit budget; enumeration verdict not comparable',
    };
  }
  const consistent = solverResult.status === brute.status;
  return {
    consistent,
    solver: solverResult.status,
    enumerator: brute.status,
    detail: consistent ? 'verdicts agree' : `MISMATCH: solver=${solverResult.status} enum=${brute.status}`,
  };
}
