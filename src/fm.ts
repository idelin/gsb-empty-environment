import { Rational } from "./rational.ts";
import { dot } from "./system.ts";
import type { Ineq } from "./system.ts";

/**
 * Exact Fourier-Motzkin elimination over rationals.
 *
 * - Feasible  -> a concrete rational witness (back-substitution).
 * - Infeasible-> a Farkas certificate: non-negative multipliers over the
 *                source inequalities deriving `0 <= c<0` (or `0 < c<=0`).
 * - Budget    -> if the inequality count would exceed `maxInequalities`,
 *                the result is "undecided" instead of a guessed verdict.
 */

export interface Budget {
  readonly maxInequalities: number;
}

export const DEFAULT_BUDGET: Budget = { maxInequalities: 20000 };

export interface FarkasCertificate {
  readonly multipliers: readonly { source: string; multiplier: Rational }[];
  /** Derived contradiction: 0 <= derivedRhs (< 0), or 0 < derivedRhs (<= 0). */
  readonly derivedRhs: Rational;
  readonly derivedStrict: boolean;
}

export type FeasibilityResult =
  | { readonly kind: "feasible"; readonly witness: readonly Rational[] }
  | { readonly kind: "infeasible"; readonly certificate: FarkasCertificate }
  | { readonly kind: "undecided"; readonly reason: string };

function scaleIneq(ineq: Ineq, factor: Rational): Ineq {
  const combo = new Map<string, Rational>();
  for (const [k, v] of ineq.combo) combo.set(k, v.mul(factor));
  return {
    coeffs: ineq.coeffs.map((c) => c.mul(factor)),
    rhs: ineq.rhs.mul(factor),
    strict: ineq.strict,
    combo,
  };
}

function addIneqs(a: Ineq, b: Ineq): Ineq {
  const coeffs = a.coeffs.map((c, i) => c.add(b.coeffs[i]));
  const combo = new Map<string, Rational>(a.combo);
  for (const [k, v] of b.combo) {
    const prev = combo.get(k);
    combo.set(k, prev === undefined ? v : prev.add(v));
  }
  return {
    coeffs,
    rhs: a.rhs.add(b.rhs),
    strict: a.strict || b.strict,
    combo,
  };
}

interface Stage {
  readonly variable: number;
  readonly lowers: readonly Ineq[]; // coeff < 0: variable >= ...
  readonly uppers: readonly Ineq[]; // coeff > 0: variable <= ...
}

export function feasible(
  inequalities: readonly Ineq[],
  numVars: number,
  budget: Budget = DEFAULT_BUDGET,
): FeasibilityResult {
  let current: Ineq[] = [...inequalities];
  const stages: Stage[] = [];

  for (let v = 0; v < numVars; v++) {
    const lowers: Ineq[] = [];
    const uppers: Ineq[] = [];
    const rest: Ineq[] = [];
    for (const ineq of current) {
      const s = ineq.coeffs[v].sign();
      if (s > 0) uppers.push(ineq);
      else if (s < 0) lowers.push(ineq);
      else rest.push(ineq);
    }
    stages.push({ variable: v, lowers, uppers });

    const next: Ineq[] = [...rest];
    for (const lo of lowers) {
      for (const up of uppers) {
        // lo: a_lo*x + ... <= b_lo (a_lo<0); up: a_up*x + ... <= b_up (a_up>0)
        // combine: |a_lo|*up + a_up*lo eliminates x.
        const combined = addIneqs(
          scaleIneq(up, lo.coeffs[v].abs()),
          scaleIneq(lo, up.coeffs[v]),
        );
        next.push(combined);
      }
    }
    if (next.length > budget.maxInequalities) {
      return {
        kind: "undecided",
        reason:
          `budget exceeded while eliminating variable ${v}: ` +
          `${next.length} inequalities > maxInequalities=${budget.maxInequalities}`,
      };
    }
    current = next;
  }

  // All variables eliminated: every remaining inequality is 0 (</<=) rhs.
  for (const ineq of current) {
    const violated = ineq.strict
      ? ineq.rhs.lte(Rational.zero())
      : ineq.rhs.lt(Rational.zero());
    if (violated) {
      const multipliers = [...ineq.combo.entries()]
        .filter(([, m]) => m.sign() > 0)
        .map(([source, multiplier]) => ({ source, multiplier }));
      return {
        kind: "infeasible",
        certificate: {
          multipliers,
          derivedRhs: ineq.rhs,
          derivedStrict: ineq.strict,
        },
      };
    }
  }

  // Back-substitution: assign variables in reverse elimination order.
  const x: Rational[] = Array.from({ length: numVars }, () => Rational.zero());
  for (let s = stages.length - 1; s >= 0; s--) {
    const stage = stages[s];
    const v = stage.variable;
    let maxLower: Rational | null = null;
    let lowerStrict = false;
    let minUpper: Rational | null = null;
    let upperStrict = false;

    const evalBound = (ineq: Ineq): Rational => {
      // ineq: a_v*x_v + sum_{j>v} a_j x_j <= rhs  ->  isolate x_v.
      let restSum = Rational.zero();
      for (let j = v + 1; j < numVars; j++) {
        restSum = restSum.add(ineq.coeffs[j].mul(x[j]));
      }
      return ineq.rhs.sub(restSum).div(ineq.coeffs[v]);
    };

    for (const lo of stage.lowers) {
      const bound = evalBound(lo); // x_v >= bound (dividing by negative flips)
      if (maxLower === null || bound.gt(maxLower)) {
        maxLower = bound;
        lowerStrict = lo.strict;
      } else if (bound.eq(maxLower)) {
        lowerStrict = lowerStrict || lo.strict;
      }
    }
    for (const up of stage.uppers) {
      const bound = evalBound(up); // x_v <= bound
      if (minUpper === null || bound.lt(minUpper)) {
        minUpper = bound;
        upperStrict = up.strict;
      } else if (bound.eq(minUpper)) {
        upperStrict = upperStrict || up.strict;
      }
    }

    if (maxLower !== null && minUpper !== null) {
      if (maxLower.gt(minUpper)) {
        return { kind: "undecided", reason: "internal: empty back-substitution interval" };
      }
      if (maxLower.eq(minUpper) && (lowerStrict || upperStrict)) {
        return { kind: "undecided", reason: "internal: strict boundary collapse" };
      }
      x[v] = maxLower.add(minUpper).div(Rational.of(2n));
    } else if (minUpper !== null) {
      x[v] = upperStrict ? minUpper.sub(Rational.one()) : minUpper;
    } else if (maxLower !== null) {
      x[v] = lowerStrict ? maxLower.add(Rational.one()) : maxLower;
    } else {
      x[v] = Rational.zero();
    }
  }

  return { kind: "feasible", witness: x };
}

/**
 * Verify a Farkas certificate against the source inequalities:
 * the non-negative combination must derive 0 (</<=) derivedRhs, which must
 * itself be a contradiction. Returns true iff the certificate is valid.
 */
export function verifyFarkas(
  sources: ReadonlyMap<string, Ineq>,
  cert: FarkasCertificate,
  numVars: number,
): boolean {
  let coeffs: Rational[] = Array.from({ length: numVars }, () => Rational.zero());
  let rhs = Rational.zero();
  let strict = false;
  for (const { source, multiplier } of cert.multipliers) {
    if (multiplier.sign() < 0) return false; // Farkas multipliers must be >= 0
    const src = sources.get(source);
    if (!src) return false;
    coeffs = coeffs.map((c, i) => c.add(src.coeffs[i].mul(multiplier)));
    rhs = rhs.add(src.rhs.mul(multiplier));
    if (multiplier.sign() > 0 && src.strict) strict = true;
  }
  if (!coeffs.every((c) => c.isZero())) return false;
  if (!rhs.eq(cert.derivedRhs) || strict !== cert.derivedStrict) return false;
  return strict ? rhs.lte(Rational.zero()) : rhs.lt(Rational.zero());
}

/** Verify a feasibility witness against the full inequality list. */
export function verifyWitness(
  inequalities: readonly Ineq[],
  witness: readonly Rational[],
): boolean {
  return inequalities.every((ineq) => {
    const lhs = dot(ineq.coeffs, witness);
    return ineq.strict ? lhs.lt(ineq.rhs) : lhs.lte(ineq.rhs);
  });
}
