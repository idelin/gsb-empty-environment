/**
 * Equivalence checker for fixed-width bit-vector expressions.
 *
 * Workflow:
 *   1. Bit-blast both expressions into one CNF "miter" (symbolic constraints).
 *   2. Solve: UNSAT means the expressions are equivalent at this width (a
 *      proof, not a sampling argument).
 *   3. SAT: minimize the model bit by bit to obtain the lexicographically
 *      smallest counterexample (variables ordered by name, each MSB-first,
 *      values unsigned), then validate it with the independent interpreter.
 *   4. If the solver budget is exhausted at any point, report "unknown".
 */

import { collectVars, type Expr } from './expr.ts';
import { evaluate } from './interp.ts';
import { BudgetExhausted, SatSolver } from './sat.ts';
import { BitBlaster, type BitVec, type Lit } from './bitblast.ts';

export const DEFAULT_BUDGET = 1_000_000;

export type CheckResult =
  | { readonly status: 'equivalent' }
  | { readonly status: 'not-equivalent'; readonly counterexample: Readonly<Record<string, bigint>> }
  | { readonly status: 'unknown' };

export interface CheckOptions {
  /** Bit width shared by both expressions; every result is truncated to it. */
  readonly width: number;
  /** Maximum SAT propagation steps before giving up with "unknown". */
  readonly budget?: number;
  /** Re-check a found counterexample with the interpreter (default: true). */
  readonly validate?: boolean;
}

export function checkEquivalence(lhs: Expr, rhs: Expr, options: CheckOptions): CheckResult {
  const { width } = options;
  if (!Number.isInteger(width) || width < 1) {
    throw new RangeError(`width must be a positive integer, got ${width}`);
  }
  const budget = { remaining: options.budget ?? DEFAULT_BUDGET };
  try {
    const solver = new SatSolver(budget);
    const bb = new BitBlaster(solver);

    // Deterministic variable order: sorted by name. The counterexample is
    // minimized lexicographically over (name, bit) pairs, MSB first.
    const varNames = [...collectVars(lhs), ...collectVars(rhs)]
      .filter((name, i, all) => all.indexOf(name) === i)
      .sort();
    const env = new Map<string, BitVec>();
    const orderedInputLits: Lit[] = [];
    for (const name of varNames) {
      const bits: BitVec = [];
      for (let i = 0; i < width; i++) bits.push(solver.newVar());
      env.set(name, bits);
      for (let i = width - 1; i >= 0; i--) orderedInputLits.push(bits[i]);
    }

    const a = bb.compile(lhs, env, width);
    const b = bb.compile(rhs, env, width);

    // Miter: at least one output bit differs.
    let differs: Lit = bb.falseLit;
    for (let i = 0; i < width; i++) {
      differs = bb.or(differs, bb.xor(a[i], b[i]));
    }
    solver.addClause([differs]);

    if (!solver.solve()) return { status: 'equivalent' };

    // Minimize: try to force each input bit to 0 in lexicographic order.
    const assumptions: Lit[] = [];
    for (const lit of orderedInputLits) {
      assumptions.push(-lit);
      if (!solver.solve(assumptions)) {
        assumptions.pop();
        assumptions.push(lit);
        if (!solver.solve(assumptions)) {
          throw new Error('internal error: lost the satisfying assignment');
        }
      }
    }

    const counterexample: Record<string, bigint> = {};
    for (const name of varNames) {
      const bits = env.get(name) as BitVec;
      let value = 0n;
      for (let i = 0; i < width; i++) {
        if (solver.valueOf(bits[i])) value |= 1n << BigInt(i);
      }
      counterexample[name] = value;
    }

    if (options.validate !== false) {
      const concreteEnv = new Map(Object.entries(counterexample));
      const va = evaluate(lhs, concreteEnv, width);
      const vb = evaluate(rhs, concreteEnv, width);
      if (va === vb) {
        throw new Error(
          `internal error: counterexample does not falsify (both sides = ${va})`,
        );
      }
    }

    return { status: 'not-equivalent', counterexample };
  } catch (err) {
    if (err instanceof BudgetExhausted) return { status: 'unknown' };
    throw err;
  }
}
