/**
 * Fractional-part expansion with repeating-cycle detection, an explicit
 * output budget, pausable/resumable output, and independent cycle
 * verification.
 *
 * Expansion algorithm (exact, BigInt-only):
 *   r <- r * base; digit <- r / den; r <- r mod den
 * A remainder of 0 means the expansion TERMINATES. A remainder seen before
 * means the digits since its first occurrence form the CYCLE. Remainders
 * are bounded by `den`, so one of the two must happen within `den` steps;
 * the budget only decides whether WE stop first.
 *
 * Budget rule: `budget` caps the number of fractional digits emitted.
 * If the budget is exhausted before termination or cycle detection, the
 * result is explicitly marked INCOMPLETE (status 'budget-exhausted') —
 * never silently presented as final.
 *
 * Rounding rule (explicit): rounding only applies to budget-truncated
 * output. 'truncate' drops the tail. 'nearest' peeks one digit past the
 * cut and rounds the magnitude half-up: it increments the last kept digit
 * iff 2 * nextDigit >= base (carry may propagate into the integer part).
 */

import { buildLookup, resolveCharset } from "./charset.ts";
import { Rational } from "./rational.ts";

export type ExpansionStatus =
  | "running"
  | "paused"
  | "terminated"
  | "cycle"
  | "budget-exhausted";

export interface Snapshot {
  remainder: string;
  digits: string;
  seen: Array<[string, number]>;
  steps: number;
  status: ExpansionStatus;
}

export class FractionExpander {
  readonly den: bigint;
  readonly base: number;
  readonly charset: string;
  readonly budget: number;

  private remainder: bigint;
  private digits = "";
  private seen = new Map<bigint, number>();
  private steps = 0;
  private status: ExpansionStatus = "running";
  private cycleStart = -1;

  /**
   * @param remainder numerator remainder r with 0 <= r < den (magnitude only)
   * @param den       positive denominator
   * @param budget    max fractional digits to emit (Infinity allowed)
   */
  constructor(remainder: bigint, den: bigint, base: number, charset: string, budget = Infinity) {
    if (remainder < 0n || remainder >= den) throw new RangeError("remainder must satisfy 0 <= r < den");
    this.den = den;
    this.base = base;
    this.charset = resolveCharset(charset, base);
    this.budget = budget;
    this.remainder = remainder;
  }

  /** Advance exactly one digit. Returns the digit char, or null when finished/blocked. */
  step(): string | null {
    if (this.status !== "running") return null;
    if (this.remainder === 0n) {
      this.status = "terminated";
      return null;
    }
    const hit = this.seen.get(this.remainder);
    if (hit !== undefined) {
      this.cycleStart = hit;
      this.status = "cycle";
      return null;
    }
    // Termination/cycle checks above emit nothing; only digit emission
    // consumes the budget.
    if (this.steps >= this.budget) {
      this.status = "budget-exhausted";
      return null;
    }
    this.seen.set(this.remainder, this.steps);
    const b = BigInt(this.base);
    const scaled = this.remainder * b;
    const d = scaled / this.den;
    this.remainder = scaled - d * this.den;
    this.digits += this.charset[Number(d)];
    this.steps++;
    return this.digits[this.digits.length - 1];
  }

  /** Run until done, paused, or maxSteps more digits were produced. */
  run(maxSteps = Infinity): void {
    let produced = 0;
    while (this.status === "running" && produced < maxSteps) {
      if (this.step() === null) break;
      produced++;
    }
  }

  pause(): void {
    if (this.status === "running") this.status = "paused";
  }

  resume(): void {
    if (this.status === "paused") this.status = "running";
  }

  getStatus(): ExpansionStatus {
    return this.status;
  }

  getSteps(): number {
    return this.steps;
  }

  /** Digits before the cycle (the transient prefix). */
  getPrefix(): string {
    if (this.status === "cycle") return this.digits.slice(0, this.cycleStart);
    return this.digits;
  }

  /** The repeating cycle, or "" if none was established. */
  getCycle(): string {
    if (this.status === "cycle") return this.digits.slice(this.cycleStart);
    return "";
  }

  getDigits(): string {
    return this.digits;
  }

  /** Serializable snapshot for pause/resume across processes. */
  snapshot(): Snapshot {
    return {
      remainder: this.remainder.toString(),
      digits: this.digits,
      seen: [...this.seen.entries()].map(([r, i]) => [r.toString(), i]),
      steps: this.steps,
      status: this.status,
    };
  }

  static restore(
    snap: Snapshot,
    den: bigint,
    base: number,
    charset: string,
    budget = Infinity,
  ): FractionExpander {
    const ex = new FractionExpander(BigInt(snap.remainder), den, base, charset, budget);
    ex.digits = snap.digits;
    ex.seen = new Map(snap.seen.map(([r, i]) => [BigInt(r), i]));
    ex.steps = snap.steps;
    ex.status = snap.status;
    return ex;
  }
}

export interface Verification {
  valid: boolean;
  minimalCycle: boolean;
  canonicalStart: boolean;
  reason: string;
}

/**
 * Independently verify a claimed (prefix, cycle) expansion of a fractional
 * rational 0 <= f < 1 in a given base — WITHOUT re-running the expansion.
 *
 * Closed form: value = P / b^p + C / (b^p * (b^c - 1)), where P and C are
 * the integer values of the prefix/cycle digit strings, p and c their
 * lengths (c = 0 means a terminating expansion).
 *
 * Also checks: cycle minimality (no smaller period) and canonical start
 * (the cycle cannot be rotated one step earlier into the prefix).
 */
export function verifyFractionDigits(
  frac: Rational,
  base: number,
  charset: string,
  prefix: string,
  cycle: string,
): Verification {
  const cs = resolveCharset(charset, base);
  const lookup = buildLookup(cs, base);
  if (frac.isNegative || frac.num >= frac.den) {
    return { valid: false, minimalCycle: false, canonicalStart: false, reason: "frac must be in [0, 1)" };
  }
  const parseDigits = (s: string): bigint | null => {
    let v = 0n;
    for (const ch of s) {
      const d = lookup.get(ch);
      if (d === undefined) return null;
      v = v * BigInt(base) + BigInt(d);
    }
    return v;
  };
  const P = parseDigits(prefix);
  const C = parseDigits(cycle);
  if (P === null || C === null) {
    return { valid: false, minimalCycle: false, canonicalStart: false, reason: "invalid digit in prefix/cycle" };
  }
  const b = BigInt(base);
  const p = BigInt(prefix.length);
  const bp = b ** p;
  // value as rational: num/den
  let num: bigint;
  let den: bigint;
  if (cycle.length === 0) {
    num = P;
    den = bp;
  } else {
    const c = BigInt(cycle.length);
    const bc = b ** c;
    num = P * (bc - 1n) + C;
    den = bp * (bc - 1n);
  }
  const claimed = new Rational(num, den);
  if (!claimed.equals(frac)) {
    return {
      valid: false,
      minimalCycle: false,
      canonicalStart: false,
      reason: `value mismatch: prefix/cycle denote ${claimed.toString()}, expected ${frac.toString()}`,
    };
  }
  // Minimality: no proper divisor period repeats inside the cycle.
  let minimal = true;
  const L = cycle.length;
  for (let d = 1; d <= L / 2 && minimal; d++) {
    if (L % d !== 0) continue;
    let ok = true;
    for (let i = d; i < L && ok; i++) if (cycle[i] !== cycle[i % d]) ok = false;
    if (ok) minimal = false;
  }
  // Canonical start: cycle must not be shiftable one digit into the prefix.
  let canonical = true;
  if (prefix.length > 0 && cycle.length > 0 && prefix[prefix.length - 1] === cycle[cycle.length - 1]) {
    canonical = false;
  }
  return {
    valid: true,
    minimalCycle: minimal,
    canonicalStart: canonical,
    reason: minimal && canonical ? "ok" : !minimal ? "cycle is not minimal" : "cycle can start one digit earlier",
  };
}

/** Deterministically re-expand prefix+cycle into `count` digits (for cross-checking). */
export function expandCycle(prefix: string, cycle: string, count: number): string {
  let out = prefix.slice(0, count);
  let i = 0;
  while (out.length < count && cycle.length > 0) {
    out += cycle[i % cycle.length];
    i++;
  }
  return out.slice(0, count);
}
