/**
 * Interval targets: the "true" value is only known to lie in [lower, upper]
 * (e.g. a measurement whose precision can be refined later). A definite
 * answer is returned only when EVERY target in the interval shares the same
 * optimal candidate; otherwise the live candidates plus the undecided
 * evidence are returned, so the caller can refine the interval and retry.
 *
 * Decisiveness theorem (exact, BigInt-only):
 *   decided  <=>  bestApprox(lower) == bestApprox(upper)
 * Proof sketch: for any two fixed candidates r, c, the set of x where r
 * beats c under the total order (error, then denominator, then numerator)
 * is a half-line — the side of the midpoint (r + c)/2 toward r. A half-line
 * that meets [lower, upper] contains the endpoint nearer to r, so any
 * challenger that wins anywhere inside already wins at an endpoint,
 * contradicting c's optimality there. Hence one endpoint comparison
 * settles the whole interval — no scan of the denominator space is needed,
 * which keeps astronomically large maxDen cheap.
 *
 * When the endpoints disagree, every rational p/q (q <= maxDen) inside
 * [lower, upper] is optimal at least at x = p/q itself, so the live
 * candidate set is exactly the Farey window over the interval. It is
 * enumerated with a Farey walk in O(#candidates) — never O(maxDen) — and
 * every step is charged to an optional budget; running out yields a
 * resumable `exhausted` result instead of a wrong answer.
 */

import { bestApprox } from "./bestApprox.ts";
import { cfTerms, convergents } from "./continuedFraction.ts";
import {
  abs,
  cmp,
  eq,
  make,
  sub,
  type Rational,
} from "./rational.ts";

export interface IntervalTarget {
  readonly lower: Rational;
  readonly upper: Rational;
}

export interface CandidateEvidence {
  readonly candidate: Rational;
  /** Smallest error this candidate achieves somewhere in the interval. */
  readonly bestError: Rational;
  /** Largest error this candidate suffers somewhere in the interval. */
  readonly worstError: Rational;
}

export type IntervalResult =
  | {
      readonly status: "decided";
      readonly best: Rational;
      /** Best-case error of `best` over the interval (0 if best lies inside). */
      readonly bestError: Rational;
      /** Worst-case error of `best` over the interval (attained at an endpoint). */
      readonly worstError: Rational;
    }
  | {
      readonly status: "undecided";
      readonly reason: "interval-too-wide";
      readonly candidates: readonly CandidateEvidence[];
      readonly evidence: string;
    }
  | {
      readonly status: "exhausted";
      readonly reason: "budget";
      /** Candidates found before the budget ran out (a lower bound on the live set). */
      readonly candidates: readonly CandidateEvidence[];
      readonly evidence: string;
    };

export interface IntervalOptions {
  /** Max number of Farey-walk steps before giving up. Default: unbounded. */
  readonly budget?: number;
}

export class BudgetExhausted extends Error {
  readonly stepsUsed: number;
  constructor(stepsUsed: number) {
    super(`budget exhausted after ${stepsUsed} steps`);
    this.stepsUsed = stepsUsed;
  }
}

/** Counted budget: throws BudgetExhausted once the step allowance runs out. */
export class Budget {
  private used = 0;
  private readonly limit: number | null;
  constructor(limit: number | null) {
    this.limit = limit;
  }
  step(): void {
    this.used++;
    if (this.limit !== null && this.used > this.limit) {
      throw new BudgetExhausted(this.used);
    }
  }
  get stepsUsed(): number {
    return this.used;
  }
}

/**
 * Smallest rational >= x with denominator <= maxDen, in O(len(CF(x))).
 * One-sided analogue of bestApprox: the answer is the last upper convergent
 * that fits, or the largest upper semiconvergent that still fits.
 */
export function ceilingRational(x: Rational, maxDen: bigint): Rational {
  const terms = cfTerms(x);
  let pm1 = 1n; // p_{-1} sentinel, correct predecessor for n = 0
  let qm1 = 0n; // q_{-1}
  let p = 0n;
  let q = 1n;
  let have = false;
  let index = -1;
  let lastIndex = -1;
  for (const c of convergents(terms)) {
    index++;
    if (index === 0) continue; // 1/0 sentinel
    if (c.q > maxDen) break;
    if (have) {
      pm1 = p;
      qm1 = q;
    }
    p = c.p;
    q = c.q;
    have = true;
    lastIndex = index;
  }
  if (lastIndex === terms.length) return make(p, q); // x itself fits
  // lastIndex counts the 1/0 sentinel, so the standard convergent index is
  // n = lastIndex - 1; convergents with odd n lie above x.
  if (lastIndex % 2 === 0) return make(p, q);
  // Even last convergent lies below x: jump to the largest upper semiconvergent.
  const k = (maxDen - qm1) / q;
  if (k >= 1n) return make(pm1 + k * p, qm1 + k * q);
  return make(pm1, qm1);
}

/** Next rational with denominator <= maxDen strictly after r (Farey successor). */
export function fareySuccessor(r: Rational, maxDen: bigint): Rational | null {
  const p = r.num;
  const q = r.den;
  const pMod = ((p % q) + q) % q;
  if (pMod === 0n) {
    // r is an integer; its successor is r + 1/maxDen.
    return make(p * maxDen + 1n, q * maxDen);
  }
  // Farey neighbors k/d satisfy k*q - p*d = 1, so d === -p^{-1} (mod q);
  // take the largest such d <= maxDen, then k = (p*d + 1)/q.
  let oldR = pMod;
  let rr = q;
  let oldS = 1n;
  let ss = 0n;
  while (rr !== 0n) {
    const quot = oldR / rr;
    [oldR, rr] = [rr, oldR - quot * rr];
    [oldS, ss] = [ss, oldS - quot * ss];
  }
  // oldR = gcd(pMod, q) = 1 and pMod*oldS === 1 (mod q).
  const s = ((oldS % q) + q) % q;
  const d = ((maxDen + s) / q) * q - s;
  if (d <= 0n || d > maxDen) return null;
  const k = (p * d + 1n) / q;
  return make(k, d);
}

/** Enumerate rationals with den <= maxDen in [lo, hi], ascending. Budget-counted. */
export function* rationalsInWindow(
  lo: Rational,
  hi: Rational,
  maxDen: bigint,
  budget: Budget,
): Generator<Rational> {
  let cur = ceilingRational(lo, maxDen);
  budget.step();
  while (cmp(cur, hi) <= 0) {
    yield cur;
    budget.step();
    const next = fareySuccessor(cur, maxDen);
    if (next === null) return;
    cur = next;
  }
}

function errorRange(c: Rational, target: IntervalTarget): { bestError: Rational; worstError: Rational } {
  const eLo = abs(sub(target.lower, c));
  const eHi = abs(sub(target.upper, c));
  const inside = cmp(target.lower, c) <= 0 && cmp(c, target.upper) <= 0;
  return {
    bestError: inside ? { num: 0n, den: 1n } : cmp(eLo, eHi) < 0 ? eLo : eHi,
    worstError: cmp(eLo, eHi) > 0 ? eLo : eHi,
  };
}

/**
 * Decide the best approximation shared by every target in [lower, upper].
 * Returns `decided` only when one candidate is provably optimal for all of them.
 */
export function bestApproxInterval(
  target: IntervalTarget,
  maxDen: bigint,
  options: IntervalOptions = {},
): IntervalResult {
  if (cmp(target.lower, target.upper) > 0) {
    throw new Error("interval lower bound exceeds upper bound");
  }
  const budget = new Budget(options.budget ?? null);
  const lo = bestApprox(target.lower, maxDen);
  const hi = bestApprox(target.upper, maxDen);

  const evidenceOf = (c: Rational): CandidateEvidence => ({
    candidate: c,
    ...errorRange(c, target),
  });

  if (eq(lo.best, hi.best)) {
    const { bestError, worstError } = errorRange(lo.best, target);
    return { status: "decided", best: lo.best, bestError, worstError };
  }

  // Undecided: every rational with den <= maxDen inside the interval is
  // optimal at itself, so enumerate the Farey window as the live candidate
  // set (plus the endpoint optima, which may lie just outside).
  const live: Rational[] = [];
  try {
    for (const r of rationalsInWindow(target.lower, target.upper, maxDen, budget)) {
      live.push(r);
    }
  } catch (err) {
    if (err instanceof BudgetExhausted) {
      const partial = dedupe([lo.best, ...live, hi.best]);
      return {
        status: "exhausted",
        reason: "budget",
        candidates: partial.map(evidenceOf),
        evidence:
          `budget exhausted after ${err.stepsUsed} steps with ${live.length} ` +
          `in-interval candidate(s) found so far; widen the budget or refine the interval`,
      };
    }
    throw err;
  }
  const candidates = dedupe([lo.best, ...live, hi.best]);
  return {
    status: "undecided",
    reason: "interval-too-wide",
    candidates: candidates.map(evidenceOf),
    evidence:
      `endpoints disagree: lower favors ${fmt(lo.best)} (err ${fmt(lo.error)}), ` +
      `upper favors ${fmt(hi.best)} (err ${fmt(hi.error)}); ` +
      `${live.length} candidate(s) inside the interval are each optimal at themselves; ` +
      `refine the interval`,
  };
}

function dedupe(rs: Rational[]): Rational[] {
  const out: Rational[] = [];
  for (const r of rs) if (!out.some((o) => eq(o, r))) out.push(r);
  return out;
}

function fmt(r: Rational): string {
  return r.den === 1n ? r.num.toString() : `${r.num}/${r.den}`;
}
