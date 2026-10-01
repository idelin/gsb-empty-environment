/**
 * Best rational approximation p/q (q <= maxDen) via continued fractions.
 *
 * Method: walk the convergents of x until the next denominator would exceed
 * maxDen, then JUMP directly to the best semiconvergent with
 *   t = floor((maxDen - q_{k-1}) / q_k),  candidate = (t*p_k + p_{k-1}) / (t*q_k + q_{k-1})
 * instead of stepping through semiconvergents one by one. This keeps the
 * cost O(log maxDen) Euclidean steps even for maxDen ~ 10^30 and beyond.
 * The convergent and the jumped semiconvergent are then compared exactly;
 * one of them is provably the best approximation of the first kind.
 *
 * Negative targets are solved on |x| and mirrored, so results are symmetric:
 * best(-x) = -best(x). Exactly representable targets return error 0.
 */

import { Budget, BudgetExhaustedError } from "./budget.ts";
import { compareCandidates, compareError } from "./ordering.ts";
import {
  absDiffRational,
  floorDiv,
  makeRational,
  type Rational,
} from "./rational.ts";

export interface ApproxOk {
  status: "ok";
  /** winning p/q (lowest terms) under the tie-breaking rules */
  value: Rational;
  /** |target - value| as an exact rational */
  error: Rational;
  /** true when the target is exactly representable within maxDen (error 0) */
  exact: boolean;
  /** every co-winning candidate, best first; length 1 means unique optimum */
  tied: Rational[];
}

export interface ApproxBudgetExhausted {
  status: "budget-exhausted";
  partial: { lastConvergent: Rational | null };
}

export type ApproxResult = ApproxOk | ApproxBudgetExhausted;

/** Partial quotients [a0; a1, a2, ...] of num/den (floor-based, exact). */
export function continuedFractionTerms(num: bigint, den: bigint, budget?: Budget): bigint[] {
  if (den <= 0n) throw new RangeError("denominator must be positive");
  const terms: bigint[] = [];
  let n = num;
  let d = den;
  for (;;) {
    budget?.consume();
    const a = floorDiv(n, d);
    terms.push(a);
    const r = n - a * d;
    if (r === 0n) return terms;
    n = d;
    d = r;
  }
}

interface Candidate {
  readonly p: bigint;
  readonly q: bigint;
}

interface CoreResult {
  winner: Candidate;
  tied: Candidate[];
  exact: boolean;
}

/** Requires num >= 0, den > 0, maxDen >= 1. Throws BudgetExhaustedError. */
function coreNonNegative(
  num: bigint,
  den: bigint,
  maxDen: bigint,
  budget: Budget | undefined,
  progress: { lastConvergent: Candidate | null },
): CoreResult {
  // Convergent recurrence: p_i = a_i*p_{i-1} + p_{i-2}, same for q.
  let pm2 = 0n;
  let pm1 = 1n;
  let qm2 = 1n;
  let qm1 = 0n;
  let n = num;
  let d = den;
  for (;;) {
    budget?.consume();
    const a = n / d; // n >= 0, d > 0, so `/` already floors
    const p = a * pm1 + pm2;
    const q = a * qm1 + qm2;
    const r = n - a * d;
    if (q > maxDen) {
      // Last convergent within budget is pm1/qm1. Jump to the best
      // semiconvergent between it and the (too big) next convergent.
      const conv: Candidate = { p: pm1, q: qm1 };
      const t = (maxDen - qm2) / qm1;
      if (t < 1n) return { winner: conv, tied: [conv], exact: false };
      const semi: Candidate = { p: t * pm1 + pm2, q: t * qm1 + qm2 };
      const target: Rational = { num, den };
      const convR = makeRational(conv.p, conv.q);
      const semiR = makeRational(semi.p, semi.q);
      const e = compareError(target, convR, semiR);
      if (e < 0) return { winner: conv, tied: [conv], exact: false };
      if (e > 0) return { winner: semi, tied: [semi], exact: false };
      const tied =
        compareCandidates(target, convR, semiR) <= 0 ? [conv, semi] : [semi, conv];
      return { winner: tied[0], tied, exact: false };
    }
    progress.lastConvergent = { p, q };
    if (r === 0n) {
      const exact: Candidate = { p, q };
      return { winner: exact, tied: [exact], exact: true };
    }
    pm2 = pm1;
    pm1 = p;
    qm2 = qm1;
    qm1 = q;
    n = d;
    d = r;
  }
}

export function bestApprox(target: Rational, maxDen: bigint, budget?: Budget): ApproxResult {
  if (maxDen < 1n) throw new RangeError("maxDen must be >= 1");
  const neg = target.num < 0n;
  const progress: { lastConvergent: Candidate | null } = { lastConvergent: null };
  try {
    const core = coreNonNegative(
      neg ? -target.num : target.num,
      target.den,
      maxDen,
      budget,
      progress,
    );
    const flip = (c: Candidate): Rational => makeRational(neg ? -c.p : c.p, c.q);
    const tied = core.tied
      .map(flip)
      .sort((a, b) => compareCandidates(target, a, b));
    const value = tied[0];
    return {
      status: "ok",
      value,
      error: absDiffRational(target, value),
      exact: core.exact,
      tied,
    };
  } catch (err) {
    if (err instanceof BudgetExhaustedError) {
      return {
        status: "budget-exhausted",
        partial: {
          lastConvergent: progress.lastConvergent
            ? makeRational(
                neg ? -progress.lastConvergent.p : progress.lastConvergent.p,
                progress.lastConvergent.q,
              )
            : null,
        },
      };
    }
    throw err;
  }
}
