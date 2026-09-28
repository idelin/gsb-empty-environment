/**
 * Rational -> decimal expansion with exact repeating-decimal handling.
 *
 * Notation:  1/6 = 0.1(6),  1/7 = 0.(142857),  1/2 = 0.5
 *
 * Strategy (a hybrid of number theory and remainder-state detection):
 *   1. The preperiod length is derived by number theory: strip factors 2 and 5
 *      from the reduced denominator; the length is max(v2, v5).
 *   2. The period is found by tracking long-division remainder states.
 *      A remainder seen again means the digits from its first occurrence repeat.
 *
 * Both digit production and stored remainder states are bounded by an explicit
 * budget; if the period is not found in time the result is marked "incomplete"
 * instead of expanding an unbounded decimal string.
 */

import { frac, type Fraction } from "./fraction.ts";

export type ExpansionKind = "terminating" | "repeating" | "incomplete";

export interface DecimalExpansion {
  /** canonical exact fraction the expansion describes */
  readonly value: Fraction;
  /** "terminating" or "repeating" are exact; "incomplete" is a bounded prefix only */
  readonly kind: ExpansionKind;
  readonly sign: -1 | 1;
  readonly integerPart: string;
  /** digits before the period (all of them for terminating/repeating) */
  readonly preperiod: string;
  /** repeating digits; "" unless kind === "repeating" */
  readonly period: string;
  /** number of digits after the decimal point actually materialized here */
  readonly digitsProduced: number;
  /** preperiod length by number theory (known even for incomplete expansions) */
  readonly preperiodLength: number;
  /** period length; null when unknown (incomplete) */
  readonly periodLength: number | null;
  /** remaining long-division remainder when detection stopped (debug/inspection) */
  readonly trailingRemainder: bigint;
}

export interface ExpandOptions {
  /** maximum fractional digits to produce (default 10_000) */
  maxDigits?: number;
  /**
   * Maximum number of distinct long-division remainder states stored.
   * The period of a reduced fraction p/q (q coprime to 10) is at most q-1,
   * so this bounds memory even when the digit budget is large.
   * Defaults to maxDigits + 1.
   */
  maxStates?: number;
}

const DEFAULT_MAX_DIGITS = 10_000;

function limit(name: string, v: number | undefined, fallback: number): number {
  const x = v ?? fallback;
  if (!Number.isInteger(x) || x < 1) {
    throw new RangeError(`${name} must be a positive integer, got ${v}`);
  }
  return x;
}

/**
 * Number theory: for a reduced fraction with denominator d, the terminating
 * prefix length after the decimal point is max(v2(d), v5(d)).
 * Returns { preLen, coprime } with coprime = d / (2^v2 * 5^v5).
 */
export function preperiodInfo(d: bigint): { preLen: number; coprime: bigint } {
  let v2 = 0;
  let v5 = 0;
  let rest = d;
  while (rest % 2n === 0n) {
    rest /= 2n;
    v2 += 1;
  }
  while (rest % 5n === 0n) {
    rest /= 5n;
    v5 += 1;
  }
  return { preLen: Math.max(v2, v5), coprime: rest };
}

interface Detection {
  digits: string;
  kind: ExpansionKind;
  preLen: number;
  period: string;
  periodLength: number | null;
  trailingRemainder: bigint;
}

/**
 * Run bounded long division with remainder-state cycle detection.
 * Works on the magnitude (numerator/denominator positive).
 */
function detect(n: bigint, d: bigint, maxDigits: number, maxStates: number): Detection {
  const { preLen, coprime } = preperiodInfo(d);

  if (coprime === 1n) {
    // Terminating decimal: long division must hit remainder 0 within preLen digits.
    let rem = n;
    let digits = "";
    for (let i = 0; i < preLen; i += 1) {
      if (digits.length >= maxDigits) {
        return {
          digits,
          kind: "incomplete",
          preLen,
          period: "",
          periodLength: null,
          trailingRemainder: rem,
        };
      }
      rem *= 10n;
      digits += (rem / d).toString();
      rem %= d;
    }
    if (rem !== 0n) {
      throw new Error("Internal error: terminating expansion did not reach remainder zero");
    }
    return {
      digits: digits.replace(/0+$/, ""),
      kind: "terminating",
      preLen,
      period: "",
      periodLength: 0,
      trailingRemainder: 0n,
    };
  }

  // Repeating decimal: the preperiod is exactly preLen, then the cycle starts.
  let rem = n;
  let digits = "";

  for (let i = 0; i < preLen && digits.length < maxDigits; i += 1) {
    rem *= 10n;
    digits += (rem / d).toString();
    rem %= d;
  }

  // Map remainder -> digit index where it first appeared after the preperiod.
  const seen = new Map<bigint, number>();
  while (digits.length < maxDigits && seen.size < maxStates) {
    if (rem === 0n) {
      // Defensive: theoretically unreachable when coprime !== 1 for a reduced
      // fraction, but treated as termination if it ever occurs.
      return {
        digits: digits.replace(/0+$/, ""),
        kind: "terminating",
        preLen,
        period: "",
        periodLength: 0,
        trailingRemainder: 0n,
      };
    }
    const first = seen.get(rem);
    if (first !== undefined) {
      const preperiod = digits.slice(0, first);
      const period = digits.slice(first);
      return {
        digits,
        kind: "repeating",
        preLen,
        period,
        periodLength: period.length,
        trailingRemainder: rem,
      };
    }
    seen.set(rem, digits.length);
    rem *= 10n;
    digits += (rem / d).toString();
    rem %= d;
  }

  return {
    digits,
    kind: "incomplete",
    preLen,
    period: "",
    periodLength: null,
    trailingRemainder: rem,
  };
}

export function expand(value: Fraction, options: ExpandOptions = {}): DecimalExpansion {
  const maxDigits = limit("maxDigits", options.maxDigits, DEFAULT_MAX_DIGITS);
  const maxStates = limit("maxStates", options.maxStates, maxDigits + 1);

  const negative = value.n < 0n;
  const magnitudeN = negative ? -value.n : value.n;
  const integerPart = magnitudeN / value.d;
  const fractionalN = magnitudeN % value.d;

  const detection = detect(fractionalN, value.d, maxDigits, maxStates);

  const preperiod =
    detection.kind === "repeating"
      ? detection.digits.slice(0, detection.digits.length - detection.period.length)
      : detection.digits;

  return {
    value,
    kind: detection.kind,
    sign: negative && magnitudeN !== 0n ? -1 : 1,
    integerPart: integerPart.toString(),
    preperiod,
    period: detection.period,
    digitsProduced: detection.digits.length,
    preperiodLength: detection.preLen,
    periodLength: detection.periodLength,
    trailingRemainder: detection.trailingRemainder,
  };
}

/** Render a terminating/repeating expansion in "0.1(6)" notation; incomplete ends with "...". */
export function format(e: DecimalExpansion): string {
  const sign = e.sign === -1 ? "-" : "";
  if (e.kind === "repeating") {
    const pre = e.preperiod;
    return `${sign}${e.integerPart}.${pre}(${e.period})`;
  }
  if (e.kind === "terminating") {
    const pre = e.preperiod;
    return pre === "" ? `${sign}${e.integerPart}` : `${sign}${e.integerPart}.${pre}`;
  }
  const prefix = e.preperiod;
  return prefix === "" ? `${sign}${e.integerPart}...` : `${sign}${e.integerPart}.${prefix}...`;
}

/** Convenience: exact text if known, otherwise the truncated prefix marked with "...". */
export function expandText(value: Fraction, options?: ExpandOptions): string {
  return format(expand(value, options));
}

export interface CycleLocation {
  readonly kind: ExpansionKind;
  readonly preperiodLength: number;
  readonly periodLength: number | null;
}

/** Locate prefix/period lengths without materializing the whole decimal string. */
export function locateCycle(value: Fraction, options?: ExpandOptions): CycleLocation {
  const e = expand(value, options);
  return {
    kind: e.kind,
    preperiodLength: e.preperiodLength,
    periodLength: e.periodLength,
  };
}

export interface DecimalPage {
  readonly start: number;
  readonly count: number;
  readonly digits: string;
  /** true when the requested window was not fully available under the budget */
  readonly truncated: boolean;
  /** period length if known; pages of a known period wrap forever */
  readonly periodLength: number | null;
  /** index within the period of the first returned digit (or null) */
  readonly periodOffset: number | null;
}

/**
 * Page fractional digits at 0-based index `start` (digit 0 is tenths).
 * Known repeating decimals page forever via index arithmetic; unknown ones
 * produce at most the budgeted prefix and report `truncated`.
 */
export function pageDigits(value: Fraction, start: number, count: number, options: ExpandOptions = {}): DecimalPage {
  if (!Number.isInteger(start) || start < 0) {
    throw new RangeError(`start must be a non-negative integer, got ${start}`);
  }
  if (!Number.isInteger(count) || count < 0) {
    throw new RangeError(`count must be a non-negative integer, got ${count}`);
  }

  const requestedWindow = start + count;
  const maxDigits = limit("maxDigits", options.maxDigits, Math.max(DEFAULT_MAX_DIGITS, requestedWindow));
  const maxStates = limit("maxStates", options.maxStates, maxDigits + 1);
  const e = expand(value, { maxDigits, maxStates });

  if (e.kind === "repeating") {
    const preLen = e.preperiod.length;
    const period = e.period;
    const out: string[] = [];
    for (let i = 0; i < count; i += 1) {
      const idx = start + i;
      if (idx < preLen) {
        out.push(e.preperiod[idx] ?? "0");
      } else {
        out.push(period[(idx - preLen) % period.length]);
      }
    }
    return {
      start,
      count,
      digits: out.join(""),
      truncated: false,
      periodLength: period.length,
      periodOffset: start < preLen ? null : (start - preLen) % period.length,
    };
  }

  // Terminating decimals continue with infinite zeros; incomplete ones stop.
  const available = e.preperiod;
  if (e.kind === "terminating") {
    let digits = start < available.length ? available.slice(start) : "";
    if (digits.length > count) digits = digits.slice(0, count);
    digits = digits.padEnd(count, "0");
    return { start, count, digits, truncated: false, periodLength: null, periodOffset: null };
  }
  const end = Math.min(start + count, available.length);
  const digits = start < available.length ? available.slice(start, end) : "";
  return {
    start,
    count,
    digits,
    truncated: start + count > available.length,
    periodLength: null,
    periodOffset: null,
  };
}

const TERMINATING_RE = /^([+-]?\d+)(?:\.(\d+))?$/;
const REPEATING_RE = /^([+-]?\d+)\.(\d*)\((\d+)\)$/;

/**
 * Parse an EXACT decimal rendering back into a Fraction.
 *
 * Accepted forms:
 *   "2", "-2.5", "0.1(6)", "-0.(142857)", "1.(9)"
 *
 * A parenthesized group is a repeating period, so the value is reconstructed
 * algebraically instead of truncating. Inputs ending in "..." are rejected:
 * a truncated prefix is never parsed as if it were exact.
 */
export function parseExact(text: string): Fraction {
  const src = text.trim();
  const m = REPEATING_RE.exec(src) ?? TERMINATING_RE.exec(src);
  if (!m) {
    throw new SyntaxError(`Not an exact decimal (truncated "..." is not accepted): ${text}`);
  }
  const intRaw = m[1];
  const negative = intRaw.startsWith("-");
  const intDigits = intRaw.replace(/^[+-]/, "");
  const isRepeating = REPEATING_RE.test(src);
  const pre = m[2] ?? "";
  const period = isRepeating ? m[3] : undefined;

  if (period === undefined) {
    const numerator = BigInt(intDigits + pre) * (negative ? -1n : 1n);
    const denominator = 10n ** BigInt(pre.length);
    return frac(numerator, denominator);
  }

  // (A*10^k + B*10^p + C - (A*10^k + B)) / (10^k * (10^p - 1))
  const k = BigInt(pre.length);
  const p = BigInt(period.length);
  const p10k = 10n ** k;
  const p10p = 10n ** p;
  const a = BigInt(intDigits);
  const b = pre === "" ? 0n : BigInt(pre);
  const c = BigInt(period);

  const prefixNum = a * p10k + b;
  const numerator = prefixNum * p10p + c - prefixNum;
  const denominator = p10k * (p10p - 1n);
  return frac(numerator * (negative ? -1n : 1n), denominator);
}
