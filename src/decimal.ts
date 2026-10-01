/**
 * Exact decimal type: value = coeff * 10^exp.
 * Parsing never passes through Number, so arbitrarily large/small values
 * are represented exactly.
 */
export interface Decimal {
  coeff: bigint;
  exp: number;
}

export function decimalDigits(x: bigint): number {
  const v = x < 0n ? -x : x;
  return v === 0n ? 1 : v.toString().length;
}

const DECIMAL_RE = /^([+-]?)(?:(\d+)(?:\.(\d*))?|\.(\d+))(?:[eE]([+-]?\d+))?$/;

/** Parse a decimal string ("12", "0.005", ".5", "1.5e-7", "+3", "-0") exactly. */
export function parseDecimal(input: string): Decimal {
  const m = DECIMAL_RE.exec(input.trim());
  if (!m) throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(input)}`);
  const [, sign, intPart, fracA, fracB, expPart] = m;
  const intDigits = intPart ?? '';
  const fracDigits = fracA ?? fracB ?? '';
  let exp = fracDigits.length === 0 ? 0 : -fracDigits.length; // avoid -0
  if (expPart !== undefined) {
    if (!/^[+-]?\d{1,9}$/.test(expPart)) {
      throw new RangeError(`decimal exponent out of range: ${expPart}`);
    }
    exp += parseInt(expPart, 10);
  }
  const digits = intDigits + fracDigits;
  let coeff = BigInt(digits === '' ? '0' : digits);
  if (sign === '-') coeff = -coeff;
  return { coeff, exp };
}

/** Render as a plain (non-exponential) decimal string. */
export function formatDecimal(d: Decimal): string {
  const neg = d.coeff < 0n;
  const s = (neg ? -d.coeff : d.coeff).toString();
  let out: string;
  if (d.exp >= 0) {
    out = s + '0'.repeat(d.exp);
  } else {
    const point = s.length + d.exp;
    out = point > 0
      ? s.slice(0, point) + '.' + s.slice(point)
      : '0.' + '0'.repeat(-point) + s;
  }
  return (neg ? '-' : '') + out;
}

/** Strip trailing decimal zeros from coeff (canonical form). */
export function normalizeDecimal(d: Decimal): Decimal {
  if (d.coeff === 0n) return { coeff: 0n, exp: 0 };
  let coeff = d.coeff;
  let exp = d.exp;
  while (coeff % 10n === 0n) {
    coeff /= 10n;
    exp += 1;
  }
  return { coeff, exp };
}
