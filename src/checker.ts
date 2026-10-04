// Symbolic equivalence checker.
//
// Builds a miter CNF(a) XOR CNF(b) != 0 and hands it to the SAT solver:
//   UNSAT   -> the expressions are equivalent (proof by unsatisfiability)
//   SAT     -> not equivalent; the model is then minimized into the
//              lexicographically smallest counterexample (variables ordered
//              by name, each compared as an unsigned value, MSB first)
//   unknown -> the resource budget was exhausted

import { collectVars } from './ast.ts';
import type { Expr } from './ast.ts';
import { bitblast } from './bitblast.ts';
import { CnfBuilder } from './cnf.ts';
import { exhaustiveCheck } from './exhaustive.ts';
import { evaluate } from './interp.ts';
import type { CheckResult } from './result.ts';
import { DEFAULT_BUDGET, SatSolver } from './sat.ts';
import type { SatBudget } from './sat.ts';

export function symbolicCheck(
  a: Expr,
  b: Expr,
  width: number,
  budget: Partial<SatBudget> = {},
): CheckResult {
  const vars = collectVars(a, b);
  const cnf = new CnfBuilder();
  const inputs = new Map<string, number[]>();
  for (const name of vars) {
    const bits: number[] = [];
    for (let i = 0; i < width; i++) bits.push(cnf.newVar());
    inputs.set(name, bits);
  }
  const bvA = bitblast(a, cnf, inputs, width);
  const bvB = bitblast(b, cnf, inputs, width);
  let diff = cnf.falseLit;
  for (let i = 0; i < width; i++) diff = cnf.or(diff, cnf.xor(bvA[i], bvB[i]));
  cnf.addClause([diff]);

  const solver = new SatSolver(cnf.nvars, cnf.clauses, budget);
  const first = solver.solve();
  if (first === 'unsat') return { status: 'equal' };
  if (first === 'unknown') {
    return { status: 'unknown', reason: 'SAT budget exhausted' };
  }

  // Minimize: for each variable (sorted order), force bits to 0 from MSB to
  // LSB whenever the miter stays satisfiable. This yields the input tuple
  // that is smallest in unsigned lexicographic order.
  const fixed: number[] = [];
  for (const name of vars) {
    const bits = inputs.get(name)!;
    for (let i = width - 1; i >= 0; i--) {
      const lit = bits[i];
      const attempt = solver.solve([...fixed, -lit]);
      if (attempt === 'sat') {
        fixed.push(-lit);
      } else if (attempt === 'unsat') {
        fixed.push(lit);
      } else {
        return { status: 'unknown', reason: 'SAT budget exhausted during minimization' };
      }
    }
  }
  const finalSolve = solver.solve(fixed);
  if (finalSolve !== 'sat') {
    return { status: 'unknown', reason: 'SAT budget exhausted during minimization' };
  }
  const counterexample: Record<string, bigint> = {};
  for (const name of vars) {
    const bits = inputs.get(name)!;
    let value = 0n;
    for (let i = width - 1; i >= 0; i--) {
      value = (value << 1n) | (solver.modelValue(bits[i]) ? 1n : 0n);
    }
    counterexample[name] = value;
  }

  // Independent sanity check: the counterexample must really distinguish the
  // two expressions under the concrete interpreter.
  if (evaluate(a, counterexample, width) === evaluate(b, counterexample, width)) {
    throw new Error('internal error: symbolic counterexample rejected by interpreter');
  }
  return { status: 'not-equal', counterexample };
}

export interface CheckOptions {
  width: number;
  engine?: 'symbolic' | 'exhaustive' | 'auto';
  budget?: Partial<SatBudget>;
  exhaustiveMaxBits?: number;
}

export function checkEquivalence(a: Expr, b: Expr, opts: CheckOptions): CheckResult {
  const { width } = opts;
  if (!Number.isInteger(width) || width < 1) throw new Error(`bad width ${width}`);
  const engine = opts.engine ?? 'auto';
  const maxBits = opts.exhaustiveMaxBits ?? 20;
  if (engine === 'exhaustive') {
    return exhaustiveCheck(a, b, width, maxBits);
  }
  if (engine === 'symbolic') {
    return symbolicCheck(a, b, width, opts.budget ?? DEFAULT_BUDGET);
  }
  // auto: small input spaces are proven by exhaustive enumeration with the
  // interpreter; larger ones go to the symbolic SAT engine.
  const totalBits = collectVars(a, b).length * width;
  if (totalBits <= maxBits) return exhaustiveCheck(a, b, width, maxBits);
  return symbolicCheck(a, b, width, opts.budget ?? DEFAULT_BUDGET);
}
