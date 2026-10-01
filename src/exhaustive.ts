/**
 * Exhaustive reference search: scan every denominator q = 1..maxDen and the
 * two nearest numerators floor(x*q), ceil(x*q). O(maxDen) — only practical
 * for small bounds, but independent of the continued-fraction machinery, so
 * the test-suite uses it to cross-validate optimality and tie-breaking.
 */

import { Budget, BudgetExhaustedError } from "./budget.ts";
import { compareCandidates, compareError } from "./ordering.ts";
import {
  absDiffRational,
  floorDiv,
  makeRational,
  type Rational,
} from "./rational.ts";

export interface ExhaustiveOk {
  status: "ok";
  value: Rational;
  error: Rational;
  tied: Rational[];
  checkedDenominators: bigint;
}

export interface ExhaustiveBudgetExhausted {
  status: "budget-exhausted";
  partial: { bestSoFar: Rational | null; checkedDenominators: bigint };
}

export type ExhaustiveResult = ExhaustiveOk | ExhaustiveBudgetExhausted;

export function bestApproxExhaustive(
  target: Rational,
  maxDen: bigint,
  budget?: Budget,
): ExhaustiveResult {
  if (maxDen < 1n) throw new RangeError("maxDen must be >= 1");
  let tied: Rational[] = [];
  let q = 1n;
  try {
    for (; q <= maxDen; q++) {
      budget?.consume();
      const p0 = floorDiv(target.num * q, target.den);
      for (const p of [p0, p0 + 1n]) {
        const cand = makeRational(p, q);
        if (tied.length === 0) {
          tied = [cand];
          continue;
        }
        const e = compareError(target, cand, tied[0]);
        if (e < 0) {
          tied = [cand];
        } else if (e === 0) {
          const dup = tied.some((t) => t.num === cand.num && t.den === cand.den);
          if (!dup) {
            tied.push(cand);
            tied.sort((a, b) => compareCandidates(target, a, b));
          }
        }
      }
    }
  } catch (err) {
    if (err instanceof BudgetExhaustedError) {
      return {
        status: "budget-exhausted",
        partial: { bestSoFar: tied[0] ?? null, checkedDenominators: q - 1n },
      };
    }
    throw err;
  }
  const value = tied[0];
  return {
    status: "ok",
    value,
    error: absDiffRational(target, value),
    tied,
    checkedDenominators: maxDen,
  };
}
