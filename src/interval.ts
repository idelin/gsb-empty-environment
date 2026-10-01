/**
 * Interval targets: the "true" value is only known to lie in [lo, hi] with
 * exact rational endpoints (e.g. a measurement that can be refined to more
 * digits later).
 *
 * Decision criterion (sound and complete):
 *   The optimum is constant over [lo, hi] iff the best approximation is the
 *   SAME UNIQUE candidate at both endpoints.
 *   Proof sketch: for any rival s > r, |x-r| - |x-s| is non-decreasing in x,
 *   so r beating every s > r at hi makes it win them on the whole interval;
 *   symmetrically rivals s < r are settled at lo.
 *
 * If the criterion fails we return "undecided" with the competing candidates
 * and the evidence: each candidate's exact error at both endpoints, where it
 * is optimal, and the equidistant boundary points inside the interval — i.e.
 * exactly what a refinement of the target precision has to resolve.
 */

import { bestApprox } from "./continuedFraction.ts";
import type { Budget } from "./budget.ts";
import {
  absDiffRational,
  addRational,
  cmpRational,
  eqRational,
  makeRational,
  parseDecimal,
  subRational,
  type Rational,
} from "./rational.ts";

export interface IntervalTarget {
  readonly lo: Rational;
  readonly hi: Rational;
}

export function makeInterval(lo: Rational, hi: Rational): IntervalTarget {
  if (cmpRational(lo, hi) > 0) {
    throw new RangeError("interval lo must be <= hi");
  }
  return { lo, hi };
}

/**
 * Interval for a decimal known to be correctly rounded to its last digit:
 * "3.14" -> [3.135, 3.145]. Refining precision = more digits = narrower
 * interval; re-run approximateInterval on the narrower interval.
 */
export function intervalFromRoundedDecimal(input: string): IntervalTarget {
  const s = input.trim();
  if (/[eE/]/.test(s)) {
    throw new SyntaxError("only plain decimal literals are supported");
  }
  const dot = s.indexOf(".");
  const fracDigits = dot >= 0 ? s.length - dot - 1 : 0;
  const v = parseDecimal(s);
  const half = makeRational(1n, 2n * 10n ** BigInt(fracDigits));
  return makeInterval(subRational(v, half), addRational(v, half));
}

export interface CandidateEvidence {
  candidate: Rational;
  errorAtLo: Rational;
  errorAtHi: Rational;
  optimalAtLo: boolean;
  optimalAtHi: boolean;
}

export interface IntervalDecided {
  status: "decided";
  value: Rational;
  errorAtLo: Rational;
  errorAtHi: Rational;
  worstError: Rational;
}

export interface IntervalUndecided {
  status: "undecided";
  reason: string[];
  candidates: Rational[];
  evidence: CandidateEvidence[];
  /** equidistant points between consecutive candidates that lie inside (lo, hi) */
  boundaries: Rational[];
}

export interface IntervalBudgetExhausted {
  status: "budget-exhausted";
  stage: "lo" | "hi";
}

export type IntervalResult = IntervalDecided | IntervalUndecided | IntervalBudgetExhausted;

export function approximateInterval(
  target: IntervalTarget,
  maxDen: bigint,
  budget?: Budget,
): IntervalResult {
  const rLo = bestApprox(target.lo, maxDen, budget);
  if (rLo.status === "budget-exhausted") return { status: "budget-exhausted", stage: "lo" };
  const rHi = bestApprox(target.hi, maxDen, budget);
  if (rHi.status === "budget-exhausted") return { status: "budget-exhausted", stage: "hi" };

  const sameValue = eqRational(rLo.value, rHi.value);
  if (rLo.tied.length === 1 && rHi.tied.length === 1 && sameValue) {
    const errorAtLo = absDiffRational(target.lo, rLo.value);
    const errorAtHi = absDiffRational(target.hi, rLo.value);
    const worstError = cmpRational(errorAtLo, errorAtHi) >= 0 ? errorAtLo : errorAtHi;
    return { status: "decided", value: rLo.value, errorAtLo, errorAtHi, worstError };
  }

  const byKey = new Map<string, Rational>();
  for (const c of [...rLo.tied, ...rHi.tied]) byKey.set(`${c.num}/${c.den}`, c);
  const candidates = [...byKey.values()].sort(cmpRational);
  const evidence: CandidateEvidence[] = candidates.map((c) => ({
    candidate: c,
    errorAtLo: absDiffRational(target.lo, c),
    errorAtHi: absDiffRational(target.hi, c),
    optimalAtLo: rLo.tied.some((t) => eqRational(t, c)),
    optimalAtHi: rHi.tied.some((t) => eqRational(t, c)),
  }));
  const boundaries: Rational[] = [];
  for (let i = 0; i + 1 < candidates.length; i++) {
    const a = candidates[i];
    const b = candidates[i + 1];
    const mid = makeRational(a.num * b.den + b.num * a.den, 2n * a.den * b.den);
    if (cmpRational(target.lo, mid) < 0 && cmpRational(mid, target.hi) < 0) {
      boundaries.push(mid);
    }
  }
  const reason: string[] = [];
  if (rLo.tied.length > 1) reason.push("tie at lo");
  if (rHi.tied.length > 1) reason.push("tie at hi");
  if (!sameValue) reason.push("different optima at interval endpoints");
  return { status: "undecided", reason, candidates, evidence, boundaries };
}
