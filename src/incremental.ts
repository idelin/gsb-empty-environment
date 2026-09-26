/**
 * Incremental constraint store.
 *
 * Constraints are asserted and retracted by id. Retraction fully withdraws
 * a constraint's influence: the store keeps no stale propagation state and
 * every check runs over exactly the active set.
 *
 * verifyByEnumeration() is an independent safety net: it exhaustively
 * enumerates the (finite) search space with direct evaluation — no
 * propagation — and cross-checks the propagation solver's verdict.
 */

import {
  evaluateModel,
  solve,
  type SolveOptions,
  type SolveResult,
  type SolverConstraint,
  type SolverVariable,
} from "./solver.ts";

type WithOptionalId<T> = T extends { readonly id: string } ? Omit<T, "id"> & { readonly id?: string } : T;

export type ConstraintInput = WithOptionalId<SolverConstraint>;

export type EnumerationVerdict =
  | { readonly status: "verified"; readonly spaceSize: number; readonly sat: boolean }
  | { readonly status: "skipped"; readonly reason: string; readonly spaceSize: number }
  | { readonly status: "mismatch"; readonly detail: string };

export class IncrementalSolver {
  private readonly variables: SolverVariable[];
  private readonly active = new Map<string, SolverConstraint>();
  private seq = 0;

  constructor(variables: readonly SolverVariable[]) {
    this.variables = variables.map((v) => ({ ...v }));
  }

  assert(input: ConstraintInput): string {
    const id = input.id ?? `c${this.seq++}`;
    if (this.active.has(id)) throw new Error(`constraint id "${id}" is already active`);
    this.active.set(id, { ...input, id } as SolverConstraint);
    return id;
  }

  retract(id: string): boolean {
    return this.active.delete(id);
  }

  activeConstraints(): SolverConstraint[] {
    return [...this.active.values()];
  }

  solve(options: SolveOptions = {}): SolveResult {
    return solve(this.variables, this.activeConstraints(), options);
  }

  verifyByEnumeration(maxSpace = 100_000): EnumerationVerdict {
    const constraints = this.activeConstraints();
    let spaceSize = 1;
    for (const v of this.variables) {
      spaceSize *= v.hi - v.lo + 1;
      if (spaceSize > maxSpace) {
        return { status: "skipped", reason: `search space exceeds ${maxSpace}`, spaceSize };
      }
    }
    const verdict = solve(this.variables, constraints, {
      maxNodes: spaceSize + 1_000,
      maxPropSteps: 50_000_000,
      minimizeConflict: false,
    });
    if (verdict.status === "budget-exceeded") {
      return { status: "skipped", reason: "solver exhausted its budget; nothing to cross-check", spaceSize };
    }
    const enumSat = this.enumerate(constraints);
    const solverSat = verdict.status === "sat";
    if (enumSat !== solverSat) {
      return {
        status: "mismatch",
        detail: `propagation solver says ${verdict.status} but exhaustive enumeration says ${enumSat ? "sat" : "unsat"}`,
      };
    }
    return { status: "verified", spaceSize, sat: solverSat };
  }

  private enumerate(constraints: readonly SolverConstraint[]): boolean {
    const model: Record<string, number> = {};
    const visit = (index: number): boolean => {
      if (index === this.variables.length) return evaluateModel(constraints, model);
      const variable = this.variables[index]!;
      for (let value = variable.lo; value <= variable.hi; value++) {
        model[variable.name] = value;
        if (visit(index + 1)) return true;
      }
      delete model[variable.name];
      return false;
    };
    return visit(0);
  }
}
