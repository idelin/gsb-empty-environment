/**
 * Correctly rounded fixed-point nth root over exact decimals.
 *
 * Given x = c * 10^e (c >= 0), degree n >= 1 and output scale s >= 0, let
 *   Y = x^(1/n) * 10^s   =>   Y^n = c * 10^(e + n*s) = c * 10^E.
 * The result coeff R = round(Y) is computed with exact integer arithmetic:
 *
 *   E >= 0:  Y^n = T := c * 10^E is an integer.
 *            r = floor(Y) = iroot(n, T).
 *            Y ? r+1/2  <=>  T * 2^n  ?  (2r+1)^n          (exact compare)
 *   E < 0:   Y^n = c / 10^(-E). r^n <= Y^n  <=>  r^n <= floor(c / 10^(-E)),
 *            so r = iroot(n, floor(c / 10^(-E))) — the fractional part of the
 *            radicand can never change the integer floor.
 *            Y ? r+1/2  <=>  c * 2^n  ?  (2r+1)^n * 10^(-E) (exact compare)
 *
 * Midpoint ties (Y exactly r + 1/2, i.e. the compare above is equality) are
 * decided by the rounding mode using the exact integer comparison — no
 * floating point anywhere, no premature truncation of tiny inputs.
 */
import { decimalDigits, parseDecimal } from './decimal.ts';
import type { Decimal } from './decimal.ts';
import { iroot } from './iroot.ts';

export type RoundingMode = 'floor' | 'ceil' | 'half-up' | 'half-down' | 'half-even';

export interface RootOptions {
  /** rounding rule, default 'half-even' (round half to even) */
  mode?: RoundingMode;
  /**
   * Hard budget on the decimal digit count of every big operand
   * (and on outScale). Throws RangeError when exceeded. Default 100_000.
   */
  maxDigits?: number;
}

export interface RoundedRoot {
  /** result = coeff * 10^exp */
  coeff: bigint;
  /** always -outScale */
  exp: number;
  n: number;
  scale: number;
  mode: RoundingMode;
  /** true iff the true root equals the rounded result exactly */
  exact: boolean;
  /** integer floor of Y = root * 10^scale (lower integer bracket) */
  floor: bigint;
  /** integer ceil of Y (upper integer bracket) */
  ceil: bigint;
  /** remainder of the underlying integer root: radicand - floor^n */
  scaledRemainder: bigint;
  /** certified lower bound on the true root x^(1/n) */
  lower: Decimal;
  /** certified upper bound on the true root x^(1/n) */
  upper: Decimal;
  /** certified max |result - true root| (0 when exact) */
  maxError: Decimal;
  /** iterations used by the integer root loop */
  iterations: number;
}

export type RootInput = Decimal | string | bigint;

const DEFAULT_MAX_DIGITS = 100_000;

function toDecimal(x: RootInput): Decimal {
  if (typeof x === 'string') return parseDecimal(x);
  if (typeof x === 'bigint') return { coeff: x, exp: 0 };
  return x;
}

function pow10(k: number): bigint {
  return 10n ** BigInt(k);
}

function compare(a: bigint, b: bigint): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function rootN(input: RootInput, n: number, outScale: number, options: RootOptions = {}): RoundedRoot {
  const mode = options.mode ?? 'half-even';
  const maxDigits = options.maxDigits ?? DEFAULT_MAX_DIGITS;
  if (!Number.isInteger(n) || n < 1) throw new RangeError('rootN: degree n must be an integer >= 1');
  if (!Number.isInteger(outScale) || outScale < 0) throw new RangeError('rootN: outScale must be an integer >= 0');
  if (outScale > maxDigits) {
    throw new RangeError(`rootN: outScale ${outScale} exceeds digit budget ${maxDigits}`);
  }
  const x = toDecimal(input);
  const c = x.coeff;
  if (c < 0n) throw new RangeError('rootN: negative input rejected');

  const E = x.exp + n * outScale;
  const workDigits = decimalDigits(c) + (E >= 0 ? E : -E) + n + 8;
  if (workDigits > maxDigits) {
    throw new RangeError(`rootN: working precision ${workDigits} digits exceeds budget ${maxDigits}`);
  }

  if (c === 0n) {
    return {
      coeff: 0n, exp: -outScale, n, scale: outScale, mode, exact: true,
      floor: 0n, ceil: 0n, scaledRemainder: 0n,
      lower: { coeff: 0n, exp: 0 }, upper: { coeff: 0n, exp: 0 },
      maxError: { coeff: 0n, exp: 0 }, iterations: 0,
    };
  }

  const nn = BigInt(n);
  const twoN = 2n ** nn;
  let r: bigint;
  let remainder: bigint;
  let exact: boolean;
  let cmpHalf: number; // sign of (Y - (r + 1/2)); 0 means exact midpoint tie
  let iterations: number;

  if (E >= 0) {
    const T = c * pow10(E);
    const ir = iroot(n, T);
    r = ir.root;
    remainder = ir.remainder;
    exact = ir.exact;
    iterations = ir.iterations;
    cmpHalf = compare(T * twoN, (2n * r + 1n) ** nn);
  } else {
    const p = pow10(-E);
    const q = c / p;
    const ir = iroot(n, q);
    r = ir.root;
    remainder = ir.remainder;
    iterations = ir.iterations;
    exact = r ** nn * p === c;
    cmpHalf = compare(c * twoN, (2n * r + 1n) ** nn * p);
  }

  let R: bigint;
  switch (mode) {
    case 'floor': R = r; break;
    case 'ceil': R = exact ? r : r + 1n; break;
    case 'half-up': R = cmpHalf >= 0 ? r + 1n : r; break;
    case 'half-down': R = cmpHalf > 0 ? r + 1n : r; break;
    case 'half-even':
      R = cmpHalf > 0 || (cmpHalf === 0 && (r & 1n) === 1n) ? r + 1n : r;
      break;
  }

  const s = outScale;
  let lower: Decimal;
  let upper: Decimal;
  let maxError: Decimal;
  if (exact) {
    lower = { coeff: R, exp: -s };
    upper = { coeff: R, exp: -s };
    maxError = { coeff: 0n, exp: 0 };
  } else if (mode === 'floor') {
    lower = { coeff: R, exp: -s };
    upper = { coeff: R + 1n, exp: -s };
    maxError = { coeff: 1n, exp: -s };
  } else if (mode === 'ceil') {
    lower = { coeff: R - 1n < 0n ? 0n : R - 1n, exp: -s };
    upper = { coeff: R, exp: -s };
    maxError = { coeff: 1n, exp: -s };
  } else {
    // (R +/- 1/2) * 10^-s = 5 * (2R +/- 1) * 10^(-s-1)
    lower = { coeff: R === 0n ? 0n : 5n * (2n * R - 1n), exp: -s - 1 };
    upper = { coeff: 5n * (2n * R + 1n), exp: -s - 1 };
    maxError = { coeff: 5n, exp: -s - 1 };
  }

  return {
    coeff: R, exp: -s, n, scale: s, mode, exact,
    floor: r, ceil: exact ? r : r + 1n,
    scaledRemainder: remainder,
    lower, upper, maxError, iterations,
  };
}

/**
 * Independent verification that `res` is a correctly rounded nth root of
 * `input` at `outScale`, under res.mode. Recomputes every bound with exact
 * integer power comparisons and checks the defining inequalities of the
 * rounding rule (including tie parity for half-even). Returns false on any
 * violation.
 */
export function verifyRoundedRoot(input: RootInput, n: number, outScale: number, res: RoundedRoot): boolean {
  const x = toDecimal(input);
  const c = x.coeff;
  if (c < 0n || res.coeff < 0n || res.exp !== -outScale) return false;
  const R = res.coeff;
  const E = x.exp + n * outScale;
  const nn = BigInt(n);
  const twoN = 2n ** nn;

  // sign of (Y - m) for integer m, via Y^n ? m^n
  const cmpInt = (m: bigint): number => {
    const mn = m ** nn;
    return E >= 0 ? compare(c * pow10(E), mn) : compare(c, mn * pow10(-E));
  };
  // sign of (Y - (m + 1/2)) via 2^n Y^n ? (2m+1)^n
  const cmpHalf = (m: bigint): number => {
    const mid = (2n * m + 1n) ** nn;
    return E >= 0 ? compare(c * pow10(E) * twoN, mid) : compare(c * twoN, mid * pow10(-E));
  };

  // integer bracket must straddle Y
  if (!(cmpInt(res.floor) >= 0 && cmpInt(res.floor + 1n) < 0)) return false;
  if (res.ceil !== (res.exact ? res.floor : res.floor + 1n)) return false;
  if (res.exact && cmpInt(R) !== 0) return false;

  // certified bounds: lower <= true root <= upper, result inside, and the
  // interval width is consistent with maxError (all exact integer checks)
  // sign of (trueRoot - d) for d = dc * 10^de, via x ? d^n (both sides >= 0)
  const cmpVsDecimalPow = (dc: bigint, de: number): number => {
    if (dc < 0n) return 1; // true root is >= 0 > d
    const pn = dc ** nn;
    const e1 = x.exp;
    const e2 = de * n;
    return e1 >= e2 ? compare(c * pow10(e1 - e2), pn) : compare(c, pn * pow10(e2 - e1));
  };
  if (res.lower.coeff < 0n) return false;
  if (cmpVsDecimalPow(res.lower.coeff, res.lower.exp) < 0) return false; // root < lower
  if (cmpVsDecimalPow(res.upper.coeff, res.upper.exp) > 0) return false; // root > upper
  const minExp = Math.min(-outScale, res.lower.exp, res.upper.exp, res.maxError.exp);
  const at = (coeff: bigint, exp: number): bigint => coeff * pow10(exp - minExp);
  const resC = at(R, -outScale);
  const loC = at(res.lower.coeff, res.lower.exp);
  const hiC = at(res.upper.coeff, res.upper.exp);
  const errC = at(res.maxError.coeff, res.maxError.exp);
  if (!(loC <= resC && resC <= hiC)) return false;
  if (resC - loC > errC || hiC - resC > errC) return false;

  switch (res.mode) {
    case 'floor':
      return cmpInt(R) >= 0 && cmpInt(R + 1n) < 0;
    case 'ceil':
      if (R === 0n) return cmpInt(0n) === 0;
      return cmpInt(R) <= 0 && cmpInt(R - 1n) > 0;
    case 'half-up':
      // R - 1/2 <= Y < R + 1/2
      return (R === 0n || cmpHalf(R - 1n) >= 0) && cmpHalf(R) < 0;
    case 'half-down':
      // R - 1/2 < Y <= R + 1/2
      return (R === 0n || cmpHalf(R - 1n) > 0) && cmpHalf(R) <= 0;
    case 'half-even': {
      const lo = R === 0n ? 1 : cmpHalf(R - 1n);
      const hi = cmpHalf(R);
      if (lo < 0 || hi > 0) return false;
      // exact midpoint ties must land on the even neighbour
      if (lo === 0 || hi === 0) return (R & 1n) === 0n;
      return true;
    }
  }
}
