import { Rational } from './rational.ts';
import type { Box } from './measurement.ts';
import type { LinearConstraint } from './constraint.ts';
import { normalize } from './constraint.ts';

/**
 * Joint feasibility of rational linear constraints over a shared box.
 *
 * Constraints share the SAME error variables, so feasibility is decided on
 * the joint region — never by merging per-constraint "possibly pass".
 * Method: exact Fourier-Motzkin elimination on BigInt rationals, tracking
 * provenance so that infeasibility yields a Farkas certificate (a non-negative
 * combination of source rows deriving a contradiction) and feasibility yields
 * a concrete rational witness point.
 */

export interface SourceRow {
  readonly kind: 'constraint' | 'upper-bound' | 'lower-bound';
  readonly ref: string; // constraint id or variable id
  readonly coeffs: readonly Rational[]; // length nVars
  readonly rhs: Rational;
  readonly strict: boolean;
}

interface Ineq {
  coeffs: Rational[];
  rhs: Rational;
  strict: boolean;
  prov: Rational[]; // conic-combination coefficients over source rows
}

export interface Budget {
  /** Max number of pairwise combinations generated during elimination. */
  readonly maxCombinations: number;
}

export const DEFAULT_BUDGET: Budget = { maxCombinations: 200_000 };

export type FeasibilityResult =
  | {
      readonly status: 'infeasible';
      readonly certificate: FarkasCertificate;
      readonly combinationsUsed: number;
    }
  | {
      readonly status: 'feasible';
      readonly witness: Map<string, Rational>;
      readonly combinationsUsed: number;
    }
  | {
      readonly status: 'pending';
      readonly reason: string;
      readonly combinationsUsed: number;
    };

export interface FarkasCertificate {
  /** Non-negative coefficients, one per source row (see `sources`). */
  readonly coefficients: readonly Rational[];
  /** The derived contradiction: 0 <[=] constant is false. */
  readonly constant: Rational;
  readonly strict: boolean;
  readonly sources: readonly SourceRow[];
}

export function buildSourceRows(
  constraints: readonly LinearConstraint[],
  varIds: readonly string[],
  box: Box,
): SourceRow[] {
  const rows: SourceRow[] = [];
  const indexOf = new Map(varIds.map((v, i) => [v, i]));
  for (const c of constraints) {
    const row = normalize(c);
    const coeffs = varIds.map(() => Rational.zero());
    for (const [k, a] of row.coeffs) {
      const i = indexOf.get(k);
      if (i === undefined) throw new Error(`constraint ${c.id}: unknown variable ${k}`);
      coeffs[i] = a;
    }
    rows.push({ kind: 'constraint', ref: c.id, coeffs, rhs: row.rhs, strict: row.strict });
  }
  varIds.forEach((v, i) => {
    const iv = box.get(v);
    if (iv === undefined) throw new Error(`box missing variable ${v}`);
    if (iv.hi.kind === 'finite') {
      const coeffs = varIds.map(() => Rational.zero());
      coeffs[i] = Rational.one();
      rows.push({ kind: 'upper-bound', ref: v, coeffs, rhs: iv.hi.v, strict: false });
    }
    if (iv.lo.kind === 'finite') {
      const coeffs = varIds.map(() => Rational.zero());
      coeffs[i] = Rational.one().neg();
      rows.push({ kind: 'lower-bound', ref: v, coeffs, rhs: iv.lo.v.neg(), strict: false });
    }
  });
  return rows;
}

export function checkFeasibility(
  constraints: readonly LinearConstraint[],
  varIds: readonly string[],
  box: Box,
  budget: Budget = DEFAULT_BUDGET,
): FeasibilityResult {
  const sources = buildSourceRows(constraints, varIds, box);
  let ineqs: Ineq[] = sources.map((s, i) => ({
    coeffs: [...s.coeffs],
    rhs: s.rhs,
    strict: s.strict,
    prov: sources.map((_, j) => (j === i ? Rational.one() : Rational.zero())),
  }));

  const n = varIds.length;
  const snapshots: Ineq[][] = [];
  let combinationsUsed = 0;

  for (let k = 0; k < n; k++) {
    snapshots.push(ineqs);
    const lowers: Ineq[] = [];
    const uppers: Ineq[] = [];
    const rest: Ineq[] = [];
    for (const ineq of ineqs) {
      const s = ineq.coeffs[k].sign();
      if (s > 0) uppers.push(ineq);
      else if (s < 0) lowers.push(ineq);
      else rest.push(ineq);
    }
    const next: Ineq[] = [...rest];
    for (const lo of lowers) {
      for (const up of uppers) {
        combinationsUsed++;
        if (combinationsUsed > budget.maxCombinations) {
          return {
            status: 'pending',
            reason: `budget exceeded: more than ${budget.maxCombinations} elimination combinations needed`,
            combinationsUsed,
          };
        }
        // lo has negative coeff on x_k, up positive; both multipliers positive.
        const cu = up.coeffs[k];
        const cl = lo.coeffs[k].neg();
        const coeffs = lo.coeffs.map((a, i) => a.mul(cu).add(up.coeffs[i].mul(cl)));
        const rhs = lo.rhs.mul(cu).add(up.rhs.mul(cl));
        const prov = lo.prov.map((a, i) => a.mul(cu).add(up.prov[i].mul(cl)));
        next.push({ coeffs, rhs, strict: lo.strict || up.strict, prov });
      }
    }
    ineqs = next;
  }

  // Only constants remain.
  for (const ineq of ineqs) {
    const violated = ineq.strict ? ineq.rhs.sign() <= 0 : ineq.rhs.sign() < 0;
    if (violated) {
      return {
        status: 'infeasible',
        certificate: {
          coefficients: ineq.prov,
          constant: ineq.rhs,
          strict: ineq.strict,
          sources,
        },
        combinationsUsed,
      };
    }
  }

  // Feasible: recover a witness by back-substitution.
  const assignment = new Map<string, Rational>();
  for (let k = n - 1; k >= 0; k--) {
    let lower: { v: Rational; strict: boolean } | null = null;
    let upper: { v: Rational; strict: boolean } | null = null;
    for (const ineq of snapshots[k]) {
      const ck = ineq.coeffs[k];
      if (ck.isZero()) continue;
      let restSum = Rational.zero();
      for (let j = k + 1; j < n; j++) {
        const xj = assignment.get(varIds[j]);
        if (xj !== undefined) restSum = restSum.add(ineq.coeffs[j].mul(xj));
      }
      // ck*x_k + rest <= rhs  =>  x_k <= (rhs-rest)/ck when ck>0,
      //                           x_k >= (rest-rhs)/|ck| when ck<0.
      const bound =
        ck.sign() > 0
          ? ineq.rhs.sub(restSum).div(ck)
          : restSum.sub(ineq.rhs).div(ck.neg());
      if (ck.sign() > 0) {
        if (upper === null || bound.lt(upper.v) || (bound.eq(upper.v) && ineq.strict)) {
          upper = { v: bound, strict: ineq.strict };
        }
      } else {
        if (lower === null || bound.gt(lower.v) || (bound.eq(lower.v) && ineq.strict)) {
          lower = { v: bound, strict: ineq.strict };
        }
      }
    }
    let value: Rational;
    if (lower !== null && upper !== null) {
      const cmp = lower.v.cmp(upper.v);
      if (cmp === 0) {
        if (lower.strict || upper.strict) {
          throw new Error('internal: empty strict fiber in feasible system');
        }
        value = lower.v;
      } else if (cmp < 0) {
        value = lower.v.add(upper.v).div(Rational.of(2n));
      } else {
        throw new Error('internal: inverted bounds in feasible system');
      }
    } else if (lower !== null) {
      value = lower.strict ? lower.v.add(Rational.one()) : lower.v;
    } else if (upper !== null) {
      value = upper.strict ? upper.v.sub(Rational.one()) : upper.v;
    } else {
      value = Rational.zero();
    }
    assignment.set(varIds[k], value);
  }

  return { status: 'feasible', witness: assignment, combinationsUsed };
}

/**
 * Verify a Farkas certificate: the non-negative combination of source rows
 * must have all-zero variable coefficients and a contradictory constant.
 */
export function verifyFarkas(cert: FarkasCertificate, nVars: number): boolean {
  const { coefficients, sources, constant, strict } = cert;
  if (coefficients.length !== sources.length) return false;
  for (const c of coefficients) if (c.sign() < 0) return false;
  for (let j = 0; j < nVars; j++) {
    let sum = Rational.zero();
    for (let i = 0; i < sources.length; i++) {
      sum = sum.add(coefficients[i].mul(sources[i].coeffs[j]));
    }
    if (!sum.isZero()) return false;
  }
  let rhsSum = Rational.zero();
  let anyStrict = false;
  for (let i = 0; i < sources.length; i++) {
    rhsSum = rhsSum.add(coefficients[i].mul(sources[i].rhs));
    if (coefficients[i].sign() > 0 && sources[i].strict) anyStrict = true;
  }
  if (!rhsSum.eq(constant)) return false;
  if (anyStrict !== strict) return false;
  return strict ? constant.sign() <= 0 : constant.sign() < 0;
}
