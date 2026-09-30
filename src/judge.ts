import { Rational } from './rational.ts';
import type { Box } from './measurement.ts';
import type { LinearConstraint } from './constraint.ts';
import { satisfies } from './constraint.ts';
import { classifyConstraint } from './classify.ts';
import type { SingleVerdict, Assignment } from './classify.ts';
import { checkFeasibility, DEFAULT_BUDGET } from './joint.ts';
import type { Budget, FarkasCertificate } from './joint.ts';

/**
 * Top-level judgment over a set of constraints sharing error variables.
 *
 * - pass:    every constraint holds for the WHOLE box (certificate: per-constraint
 *            worst-case corners and bounds).
 * - fail:    the joint feasible region is EMPTY (certificate: Farkas combination,
 *            or a single constraint that already fails everywhere).
 * - mixed:   feasible but not certain — certificates: a rational witness point
 *            inside the box satisfying all constraints, plus a counterexample
 *            corner violating some constraint.
 * - pending: the elimination budget was insufficient; no verdict is claimed.
 */
export type Judgment =
  | { readonly kind: 'pass'; readonly perConstraint: readonly SingleVerdict[] }
  | { readonly kind: 'fail'; readonly certificate: FailCertificate }
  | {
      readonly kind: 'mixed';
      readonly witness: Assignment;
      readonly counterexample: Counterexample;
      readonly perConstraint: readonly SingleVerdict[];
    }
  | { readonly kind: 'pending'; readonly reason: string; readonly combinationsUsed: number };

export type FailCertificate =
  | { readonly type: 'farkas'; readonly farkas: FarkasCertificate }
  | { readonly type: 'single-constraint'; readonly verdict: SingleVerdict };

export type Counterexample =
  | { readonly type: 'corner'; readonly constraintId: string; readonly corner: Assignment }
  | { readonly type: 'unbounded'; readonly constraintId: string };

export function judge(
  constraints: readonly LinearConstraint[],
  varIds: readonly string[],
  box: Box,
  budget: Budget = DEFAULT_BUDGET,
): Judgment {
  const singles = constraints.map((c) => classifyConstraint(c, box));

  // A constraint that fails everywhere already empties the joint region.
  const failing = singles.find((s) => s.status === 'fail');
  if (failing !== undefined) {
    return { kind: 'fail', certificate: { type: 'single-constraint', verdict: failing } };
  }

  if (singles.every((s) => s.status === 'pass')) {
    return { kind: 'pass', perConstraint: singles };
  }

  // Some constraints are only "possibly" satisfiable: decide on the JOINT region.
  const feasibility = checkFeasibility(constraints, varIds, box, budget);
  if (feasibility.status === 'pending') {
    return { kind: 'pending', reason: feasibility.reason, combinationsUsed: feasibility.combinationsUsed };
  }
  if (feasibility.status === 'infeasible') {
    return { kind: 'fail', certificate: { type: 'farkas', farkas: feasibility.certificate } };
  }

  const witness = feasibility.witness;
  const uncertain = singles.find((s) => s.status !== 'pass');
  let counterexample: Counterexample;
  if (uncertain !== undefined && uncertain.maxCorner !== null) {
    counterexample = { type: 'corner', constraintId: uncertain.constraintId, corner: uncertain.maxCorner };
  } else if (uncertain !== undefined) {
    counterexample = { type: 'unbounded', constraintId: uncertain.constraintId };
  } else {
    // Defensive: should be unreachable given the branches above.
    counterexample = { type: 'unbounded', constraintId: constraints[0]?.id ?? '?' };
  }
  return { kind: 'mixed', witness, counterexample, perConstraint: singles };
}

/** Exact re-check of a witness against all constraints and the box. */
export function verifyWitness(
  witness: Assignment,
  constraints: readonly LinearConstraint[],
  box: Box,
): boolean {
  for (const [varId, x] of witness) {
    const iv = box.get(varId);
    if (iv === undefined) return false;
    if (iv.lo.kind === 'finite' && x.lt(iv.lo.v)) return false;
    if (iv.hi.kind === 'finite' && x.gt(iv.hi.v)) return false;
  }
  return constraints.every((c) => satisfies(c, witness));
}
