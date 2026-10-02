/**
 * Best rational approximation p/q to a target x with q <= maxDen.
 *
 * Method: continued-fraction convergents plus a single semiconvergent
 * "jump". The optimum is always either the last convergent whose
 * denominator fits, or the largest intermediate fraction between it and the
 * next convergent that still fits — so the cost is O(len(CF(x))), never
 * O(maxDen). This is what makes denominator bounds like 10^30 cheap.
 *
 * Explicit rounding / tie-breaking rule (total order, applied everywhere):
 *   1. smaller error |x - p/q| wins;
 *   2. equal error  -> smaller denominator wins;
 *   3. still equal  -> smaller numerator wins (i.e. toward -infinity).
 * Rule 3 makes exact halves resolve downward: bestInteger(1/2) = 0,
 * bestInteger(-1/2) = -1.
 */

import { convergents, cfTerms } from "./continuedFraction.ts";
import {
  abs,
  cmp,
  eq,
  floor,
  make,
  sub,
  type Rational,
} from "./rational.ts";

export interface ApproxResult {
  readonly best: Rational;
  /** Exact error |x - best| as a normalized rational; 0/1 when x is hit exactly. */
  readonly error: Rational;
}

/** Compare two candidates under the documented total order. True if a beats b. */
export function beats(a: Rational, b: Rational, x: Rational): boolean {
  const errA = abs(sub(x, a));
  const errB = abs(sub(x, b));
  const byError = cmp(errA, errB);
  if (byError !== 0) return byError < 0;
  if (a.den !== b.den) return a.den < b.den;
  return a.num < b.num;
}

function pick(candidates: Rational[], x: Rational): Rational {
  let best = candidates[0]!;
  for (let i = 1; i < candidates.length; i++) {
    const c = candidates[i]!;
    if (beats(c, best, x)) best = c;
  }
  return best;
}

/**
 * Best approximation to x with denominator <= maxDen (maxDen >= 1).
 * Runs in O(number of CF terms of x); safe for astronomically large maxDen.
 */
export function bestApprox(x: Rational, maxDen: bigint): ApproxResult {
  if (maxDen < 1n) throw new Error("maxDen must be >= 1");
  const terms = cfTerms(x);

  // Track the last two convergents that fit, plus whether x was hit exactly.
  let pm1 = 1n; // p_{-1} sentinel, correct predecessor for n = 0
  let qm1 = 0n; // q_{-1}
  let p = 0n;
  let q = 1n;
  let have = false;
  let exact = false;
  let index = -1;
  for (const c of convergents(terms)) {
    index++;
    if (index === 0) continue; // skip the 1/0 sentinel
    if (c.q > maxDen) break;
    if (have) {
      pm1 = p;
      qm1 = q;
    }
    p = c.p;
    q = c.q;
    have = true;
    if (index === terms.length) {
      exact = true; // last convergent reproduces x and fits the bound
    }
  }

  const conv = make(p, q);
  if (exact) return { best: conv, error: { num: 0n, den: 1n } };

  // Semiconvergent jump: the largest k with q_{n-1} + k*q_n <= maxDen.
  // Because q_{n+1} = a_{n+1}*q_n + q_{n-1} > maxDen, k < a_{n+1}, so this
  // is a genuine intermediate fraction, not the next convergent.
  const k = (maxDen - qm1) / q;
  const candidates: Rational[] = [conv];
  if (k >= 1n) {
    candidates.push(make(pm1 + k * p, qm1 + k * q));
  }
  const best = pick(candidates, x);
  return { best, error: abs(sub(x, best)) };
}

/** Convenience: best integer approximation (maxDen = 1). */
export function bestInteger(x: Rational): bigint {
  return bestApprox(x, 1n).best.num;
}

/**
 * Reference implementation: exhaustive scan of every denominator 1..maxDen
 * and the (at most two) nearest numerators for each. O(maxDen) — only for
 * small bounds, used to cross-validate bestApprox and the tie-break rules.
 */
export function bestApproxExhaustive(x: Rational, maxDen: bigint): ApproxResult {
  if (maxDen < 1n) throw new Error("maxDen must be >= 1");
  if (maxDen > 10_000_000n) throw new Error("exhaustive scan refuses maxDen > 1e7");
  let best: Rational | null = null;
  for (let q = 1n; q <= maxDen; q++) {
    // Nearest numerators: floor and ceil of x*q.
    const scaled = make(x.num * q, x.den);
    const lo = floor(scaled);
    for (const n of [lo, lo + 1n]) {
      const cand = make(n, q);
      if (best === null || beats(cand, best, x)) best = cand;
    }
  }
  const b = best!;
  return { best: b, error: abs(sub(x, b)) };
}

/** All rationals tied for best under the total order's error level (diagnostic aid). */
export function tiedWith(x: Rational, a: Rational, b: Rational): boolean {
  return eq(abs(sub(x, a)), abs(sub(x, b)));
}
