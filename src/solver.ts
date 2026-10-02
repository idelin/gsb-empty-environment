/**
 * Constraint intersection solver.
 *
 * Range (exact decimal bounds), multipleOf and digit limits are intersected
 * in a scaled-integer domain: with grid unit u = 10^-g, every admissible
 * value is j*u for an integer j. multipleOf becomes a divisibility condition
 * j ≡ 0 (mod q); precision becomes a finite union of digit-length regions,
 * each an arithmetic progression. The result is either a feasible witness
 * (verified against the constraints) or precise no-solution evidence.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  DEFAULT_MAX_EXPONENT,
  ExponentBudgetError,
  canonical,
  compareDecimal,
  fromScaledInt,
  scaleOf,
  zeroDecimal,
} from "./decimal.ts";
import type { Constraints } from "./constraints.ts";
import { checkConstraints } from "./constraints.ts";

export type SolveResult =
  | { readonly feasible: true; readonly witness: Decimal; readonly canonical: string }
  | { readonly feasible: false; readonly evidence: string };

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

function lcm(a: bigint, b: bigint): bigint {
  return (a / gcd(a, b)) * b;
}

/** Smallest multiple of q that is >= j. q > 0. */
function ceilToMultiple(j: bigint, q: bigint): bigint {
  const r = j % q;
  return r === 0n ? j : j + (r > 0n ? q - r : -r);
}

/** Largest multiple of q that is <= j. q > 0. */
function floorToMultiple(j: bigint, q: bigint): bigint {
  const r = j % q;
  return r === 0n ? j : j - (r > 0n ? r : q + r);
}

/** floor(d * 10^g) as an exact integer. */
function floorToUnit(d: Decimal, g: number): bigint {
  const k = BigInt(d.exp + g);
  const s = d.neg ? -1n : 1n;
  if (k >= 0n) return s * d.coeff * 10n ** k;
  const div = 10n ** -k;
  const q = d.coeff / div;
  const r = d.coeff % div;
  return d.neg ? (r === 0n ? -q : -(q + 1n)) : q;
}

/** ceil(d * 10^g) as an exact integer. */
function ceilToUnit(d: Decimal, g: number): bigint {
  const k = BigInt(d.exp + g);
  const s = d.neg ? -1n : 1n;
  if (k >= 0n) return s * d.coeff * 10n ** k;
  const div = 10n ** -k;
  const q = d.coeff / div;
  const r = d.coeff % div;
  return d.neg ? -q : r === 0n ? q : q + 1n;
}

export interface LatticeInfo {
  /** Grid exponent: admissible values are j * 10^-g. */
  readonly g: number;
  /** Lattice step in grid units: j must be a multiple of q (>= 1). */
  readonly q: bigint;
  /** Integer bounds on j from the decimal range; null = unbounded. */
  readonly jLo: bigint | null;
  readonly jHi: bigint | null;
}

/**
 * Reduce constraints to lattice form. Throws ExponentBudgetError when the
 * required grid is finer than the budget allows — before any allocation.
 */
export function latticeInfo(c: Constraints, opts: DecimalOptions = {}): LatticeInfo | null {
  const budget = opts.maxExponent ?? DEFAULT_MAX_EXPONENT;
  const hasLattice =
    c.scale !== undefined || c.multipleOf !== undefined || c.precision !== undefined;
  if (!hasLattice) return null;
  const g = c.scale ?? (c.multipleOf !== undefined ? scaleOf(c.multipleOf) : c.precision!);
  if (g > budget) {
    throw new ExponentBudgetError(BigInt(g), budget);
  }
  let q = 1n;
  if (c.multipleOf !== undefined) {
    const m = c.multipleOf;
    const emin = Math.min(-g, m.exp);
    const uC = 10n ** BigInt(-g - emin);
    const mC = m.coeff * 10n ** BigInt(m.exp - emin);
    q = lcm(uC, mC) / uC;
  }
  let jLo: bigint | null = null;
  let jHi: bigint | null = null;
  if (c.lo !== undefined) {
    jLo = c.lo.inclusive ? ceilToUnit(c.lo.value, g) : floorToUnit(c.lo.value, g) + 1n;
  }
  if (c.hi !== undefined) {
    jHi = c.hi.inclusive ? floorToUnit(c.hi.value, g) : ceilToUnit(c.hi.value, g) - 1n;
  }
  return { g, q, jLo, jHi };
}

function nudgeUp(b: Decimal): Decimal {
  // b + 10^(b.exp - 1): the next representable value one digit finer.
  return fromScaledInt(b.coeff * 10n + 1n, b.exp - 1);
}

function nudgeDown(b: Decimal): Decimal {
  return fromScaledInt(-(b.coeff * 10n + 1n), b.exp - 1);
}

function withinBounds(v: Decimal, c: Constraints): boolean {
  if (c.lo !== undefined) {
    const cmp = compareDecimal(v, c.lo.value);
    if (cmp < 0 || (cmp === 0 && !c.lo.inclusive)) return false;
  }
  if (c.hi !== undefined) {
    const cmp = compareDecimal(v, c.hi.value);
    if (cmp > 0 || (cmp === 0 && !c.hi.inclusive)) return false;
  }
  return true;
}

function boundText(c: Constraints): string {
  const lo = c.lo === undefined ? "-inf" : canonical(c.lo.value);
  const hi = c.hi === undefined ? "+inf" : canonical(c.hi.value);
  return `${c.lo !== undefined && !c.lo.inclusive ? "(" : "["}${lo}, ${hi}${c.hi !== undefined && !c.hi.inclusive ? ")" : "]"}`;
}

/** Intersect range + multipleOf + digit limits; witness or exact evidence. */
export function solve(c: Constraints, opts: DecimalOptions = {}): SolveResult {
  const lat = latticeInfo(c, opts);

  if (lat === null) {
    // Continuous mode: any decimal in the range works.
    if (c.lo !== undefined && c.hi !== undefined) {
      const cmp = compareDecimal(c.lo.value, c.hi.value);
      if (cmp > 0 || (cmp === 0 && !(c.lo.inclusive && c.hi.inclusive))) {
        return {
          feasible: false,
          evidence: `empty decimal interval ${boundText(c)}: lower bound not below upper bound`,
        };
      }
      if (cmp === 0) return finish(c.lo.value, c);
    }
    const zero = zeroDecimal();
    if (withinBounds(zero, c)) return finish(zero, c);
    if (c.hi !== undefined && compareDecimal(c.hi.value, zero) <= 0) {
      return finish(c.hi.inclusive ? c.hi.value : nudgeDown(c.hi.value), c);
    }
    const lo = c.lo!;
    return finish(lo.inclusive ? lo.value : nudgeUp(lo.value), c);
  }

  const { g, q, jLo, jHi } = lat;
  const P = c.precision;

  const fmtBound = (v: bigint | null, def: string): string => (v === null ? def : v.toString());

  if (P === undefined) {
    // Pure lattice ∩ interval.
    if (jLo !== null && jHi !== null && jLo > jHi) {
      return {
        feasible: false,
        evidence:
          `empty integer interval: j in [${jLo}, ${jHi}] (value = j * 10^-${g}) ` +
          `derived from range ${boundText(c)}`,
      };
    }
    if ((jLo === null || jLo <= 0n) && (jHi === null || jHi >= 0n)) {
      return finish(fromScaledInt(0n, -g), c);
    }
    if (jLo !== null && jLo > 0n) {
      const j0 = ceilToMultiple(jLo, q);
      if (jHi !== null && j0 > jHi) {
        return noLatticePoint(g, q, jLo, jHi, c);
      }
      return finish(fromScaledInt(j0, -g), c);
    }
    const j0 = floorToMultiple(jHi!, q);
    if (jLo !== null && j0 < jLo) {
      return noLatticePoint(g, q, jLo, jHi, c);
    }
    return finish(fromScaledInt(j0, -g), c);
  }

  // Precision restricts j to digit-length regions. |j| < 10^(P+g) is
  // necessary, so unbounded ranges become bounded here.
  const jAbsMax = 10n ** BigInt(P + g) - 1n;
  const effLo = jLo === null ? -jAbsMax : jLo < -jAbsMax ? -jAbsMax : jLo;
  const effHi = jHi === null ? jAbsMax : jHi > jAbsMax ? jAbsMax : jHi;

  const candidates: bigint[] = [];
  if (effLo <= 0n && effHi >= 0n) candidates.push(0n);

  for (let d = 1; d <= P + g; d++) {
    const t = Math.max(0, Math.max(d, g) - P);
    const step = lcm(q, 10n ** BigInt(t));
    const dLo = 10n ** BigInt(d - 1);
    const dHi = 10n ** BigInt(d) - 1n;
    // Positive side [dLo, dHi], negative side [-dHi, -dLo].
    const pLo = effLo > dLo ? effLo : dLo;
    const pHi = effHi < dHi ? effHi : dHi;
    if (pLo <= pHi) {
      const j0 = ceilToMultiple(pLo, step);
      if (j0 <= pHi) candidates.push(j0);
    }
    const nLo = effLo > -dHi ? effLo : -dHi;
    const nHi = effHi < -dLo ? effHi : -dLo;
    if (nLo <= nHi) {
      const j0 = ceilToMultiple(nLo, step);
      if (j0 <= nHi) candidates.push(j0);
    }
  }

  if (candidates.length === 0) {
    return {
      feasible: false,
      evidence:
        `no admissible value: on grid u = 10^-${g}, range ${boundText(c)} gives ` +
        `j in [${fmtBound(jLo, "-inf")}, ${fmtBound(jHi, "+inf")}]` +
        (q > 1n ? ` with j ≡ 0 (mod ${q})` : "") +
        `; precision <= ${P} further requires j = 0 or, for each digit length ` +
        `d = 1..${P + g}, j divisible by 10^max(0, max(d, ${g}) - ${P}) ` +
        `with |j| <= ${jAbsMax}; none of these regions intersects`,
    };
  }
  // Minimal |value|, ties toward the smaller value: deterministic witness.
  let best = candidates[0]!;
  for (const j of candidates) {
    const aj = j < 0n ? -j : j;
    const ab = best < 0n ? -best : best;
    if (aj < ab || (aj === ab && j < best)) best = j;
  }
  return finish(fromScaledInt(best, -g), c);
}

function noLatticePoint(
  g: number,
  q: bigint,
  jLo: bigint | null,
  jHi: bigint | null,
  c: Constraints,
): SolveResult {
  const lo = jLo ?? 0n;
  const hi = jHi ?? 0n;
  const below = floorToMultiple(hi, q);
  const above = ceilToMultiple(lo, q);
  return {
    feasible: false,
    evidence:
      `no lattice point: j in [${jLo ?? "-inf"}, ${jHi ?? "+inf"}] (value = j * 10^-${g}) ` +
      `with j ≡ 0 (mod ${q}); nearest multiples are ${below} below/inside and ` +
      `${above} above, both outside the interval (range ${boundText(c)})`,
  };
}

/** Verify the witness against the constraints before returning it. */
function finish(witness: Decimal, c: Constraints): SolveResult {
  const violations = checkConstraints(witness, c);
  if (violations.length > 0) {
    throw new Error(
      `internal solver error: witness ${canonical(witness)} violates ${JSON.stringify(violations)}`,
    );
  }
  return { feasible: true, witness, canonical: canonical(witness) };
}
