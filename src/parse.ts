/**
 * Literal parsing into exact values. Grammar (whitespace tolerated):
 *
 *   integer    123  -456789012345678901234567890
 *   decimal    0.1  -3.25  1.5e-3   (exact, no binary float involved)
 *   fraction   22/7  -1/3
 *   interval   1.5±0.05  or  1.5+/-0.05   (needs a provenance token)
 *
 * NaN and Infinity are rejected as unsupported: this library compares
 * finite exact numbers and finite error intervals only.
 */

import { type Rational, make, fromScaledInt } from './rational.ts';
import { type AffineValue, exactValue, uncertainValue } from './value.ts';

export class UnsupportedLiteralError extends Error {
  constructor(literal: string) {
    super(`unsupported numeric literal: ${JSON.stringify(literal)} (NaN and Infinity are not supported)`);
    this.name = 'UnsupportedLiteralError';
  }
}

export class MalformedLiteralError extends Error {
  constructor(literal: string) {
    super(`malformed numeric literal: ${JSON.stringify(literal)}`);
    this.name = 'MalformedLiteralError';
  }
}

const INT_RE = /^[+-]?\d+$/;
const DEC_RE = /^([+-]?)(\d+)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;
const DEC_FRAC_RE = /^([+-]?)\.(\d+)(?:[eE]([+-]?\d+))?$/;
const FRACTION_RE = /^([+-]?\d+)\/(\d+)$/;
const INTERVAL_RE = /^(.+?)(?:±|\+\/-)(.+)$/;

export function parseRational(text: string): Rational {
  const s = text.trim();
  if (/nan|infinity|inf/i.test(s)) throw new UnsupportedLiteralError(s);
  if (INT_RE.test(s)) return make(BigInt(s), 1n);

  const frac = FRACTION_RE.exec(s);
  if (frac) return make(BigInt(frac[1]), BigInt(frac[2]));

  const dec = DEC_RE.exec(s);
  if (dec) {
    const sign = dec[1] === '-' ? -1n : 1n;
    const intDigits = dec[2];
    const fracDigits = dec[3] ?? '';
    const exp = dec[4] !== undefined ? parseInt(dec[4], 10) : 0;
    const mantissa = sign * BigInt(intDigits + fracDigits);
    return fromScaledInt(mantissa, exp - fracDigits.length);
  }
  const decFrac = DEC_FRAC_RE.exec(s);
  if (decFrac) {
    const sign = decFrac[1] === '-' ? -1n : 1n;
    const fracDigits = decFrac[2];
    const exp = decFrac[3] !== undefined ? parseInt(decFrac[3], 10) : 0;
    return fromScaledInt(sign * BigInt(fracDigits), exp - fracDigits.length);
  }
  throw new MalformedLiteralError(s);
}

let anonymousTokenCounter = 0;

/**
 * Parse a literal into an AffineValue. Interval literals ("a±b") require
 * a provenance token so their error can be correlated (or not) with
 * other values from the same source; without one, a fresh anonymous
 * token is minted, which is conservatively treated as independent.
 */
export function parseValue(text: string, provenance?: string): AffineValue {
  const s = text.trim();
  if (/nan|infinity|inf/i.test(s)) throw new UnsupportedLiteralError(s);
  const interval = INTERVAL_RE.exec(s);
  if (interval) {
    const centre = parseRational(interval[1]);
    const radius = parseRational(interval[2]);
    const token = provenance ?? `anon#${++anonymousTokenCounter}`;
    return uncertainValue(centre, token, radius);
  }
  return exactValue(parseRational(s));
}
