/**
 * Incremental constraint store.
 *
 * Constraints are added and retracted by id. Retraction fully withdraws a
 * constraint's influence: the store re-solves from the active set only, so
 * no stale propagation effects survive. `checkVerified` additionally runs
 * the independent enumerator to confirm propagation safety.
 */

import { enumerateAll, verifyAgainstEnumeration, type Verification } from './enumerator.ts';
import { solve, type Constraint, type SolveOptions, type SolveResult, type VarDecl } from './solver.ts';

export class ConstraintStore {
  private vars: VarDecl[];
  private active = new Map<string, Constraint>();

  constructor(vars: VarDecl[]) {
    this.vars = vars;
  }

  add(id: string, constraint: Constraint): void {
    this.active.set(id, constraint);
  }

  retract(id: string): boolean {
    return this.active.delete(id);
  }

  get size(): number {
    return this.active.size;
  }

  constraints(): Constraint[] {
    return [...this.active.values()];
  }

  check(opts: SolveOptions = {}): SolveResult {
    return solve(this.vars, this.constraints(), opts);
  }

  /** Solve, then cross-check the verdict with the independent enumerator. */
  checkVerified(opts: SolveOptions = {}): { result: SolveResult; verification: Verification } {
    const result = this.check(opts);
    const verification = verifyAgainstEnumeration(result, this.vars, this.constraints());
    return { result, verification };
  }
}

export interface ConflictCore {
  /** Subset-minimal set of constraint ids that is still unsatisfiable. */
  core: string[];
  /** False if any sub-solve hit the budget, so minimality is not certified. */
  exact: boolean;
}

/**
 * Deletion-based subset-minimal conflict extraction: repeatedly drop a
 * constraint; if the remainder is still unsat, the drop is kept. The result
 * is irreducible (removing any member makes it satisfiable), provided the
 * budget was never exhausted along the way.
 */
export function minimizeConflict(
  vars: VarDecl[],
  entries: [string, Constraint][],
  opts: SolveOptions = {},
): ConflictCore {
  let current = [...entries];
  let exact = true;
  let i = 0;
  while (i < current.length) {
    const candidate = [...current.slice(0, i), ...current.slice(i + 1)];
    const r = solve(vars, candidate.map(([, c]) => c), opts);
    if (r.status === 'unknown') {
      exact = false;
      i++; // cannot prove redundancy: keep the constraint
      continue;
    }
    if (r.status === 'unsat') {
      current = candidate; // constraint was redundant: drop it for good
    } else {
      i++;
    }
  }
  return { core: current.map(([id]) => id), exact };
}

/** Convenience: solve once and, if unsat, extract the minimal conflict. */
export function explainUnsat(
  vars: VarDecl[],
  entries: [string, Constraint][],
  opts: SolveOptions = {},
): { result: SolveResult; conflict?: ConflictCore } {
  const result = solve(vars, entries.map(([, c]) => c), opts);
  if (result.status !== 'unsat') return { result };
  return { result, conflict: minimizeConflict(vars, entries, opts) };
}

export { enumerateAll };
