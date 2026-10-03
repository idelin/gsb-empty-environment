/**
 * Independent reference implementation used to cross-check the main engine
 * ("finite scaled-integer reference"). Values are exact rationals num/den
 * with den a power of ten, implemented naively and sharing no code with
 * src/decimal.ts. Intended for small, finite test windows.
 */

import type { DecimalSchema } from "./schema.ts";

export interface Rat {
  num: bigint;
  den: bigint; // always 10^k, k >= 0
}

const RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

export function parseRat(text: string): Rat {
  const m = RE.exec(text);
  if (!m) throw new Error(`reference: bad literal ${JSON.stringify(text)}`);
  const [, s, i, f = "", e] = m;
  const exp = BigInt(e ?? "0");
  let num = BigInt(i + f);
  let den = 10n ** BigInt(f.length);
  if (exp >= 0n) num *= 10n ** exp;
  else den *= 10n ** -exp;
  if (s === "-") num = -num;
  return { num, den };
}

function cmpRat(a: Rat, b: Rat): number {
  const l = a.num * b.den;
  const r = b.num * a.den;
  return l < r ? -1 : l > r ? 1 : 0;
}

/** Positional total digits, computed naively on the reduced rational. */
export function ratTotalDigits(r: Rat): number {
  let num = r.num < 0n ? -r.num : r.num;
  let den = r.den;
  if (num === 0n) return 1;
  while (num % 10n === 0n && den % 10n === 0n) {
    num /= 10n;
    den /= 10n;
  }
  // After cancelling common factors of 10, the remaining numerator's digit
  // count is exactly the positional digit count (den = 10^k only rescales).
  return num.toString().length;
}

/** Fraction digits of the reduced rational. */
export function ratFractionDigits(r: Rat): number {
  let num = r.num < 0n ? -r.num : r.num;
  let den = r.den;
  if (num === 0n) return 0;
  while (num % 10n === 0n && den % 10n === 0n) {
    num /= 10n;
    den /= 10n;
  }
  return den.toString().length - 1;
}

/** v is an integer multiple of m (m != 0). */
export function ratIsMultiple(v: Rat, m: Rat): boolean {
  const lhs = v.num * m.den;
  const rhs = v.den * (m.num < 0n ? -m.num : m.num);
  const a = lhs < 0n ? -lhs : lhs;
  return a % rhs === 0n;
}

export interface CompiledReference {
  minimum?: Rat;
  maximum?: Rat;
  exclusiveMinimum?: Rat;
  exclusiveMaximum?: Rat;
  multipleOf?: Rat;
  maxTotalDigits?: number;
  maxFractionDigits?: number;
}

export function compileReference(schema: DecimalSchema): CompiledReference {
  const out: CompiledReference = {};
  if (schema.minimum !== undefined) out.minimum = parseRat(schema.minimum);
  if (schema.maximum !== undefined) out.maximum = parseRat(schema.maximum);
  if (schema.exclusiveMinimum !== undefined) out.exclusiveMinimum = parseRat(schema.exclusiveMinimum);
  if (schema.exclusiveMaximum !== undefined) out.exclusiveMaximum = parseRat(schema.exclusiveMaximum);
  if (schema.multipleOf !== undefined) out.multipleOf = parseRat(schema.multipleOf);
  if (schema.maxTotalDigits !== undefined) out.maxTotalDigits = schema.maxTotalDigits;
  if (schema.maxFractionDigits !== undefined) out.maxFractionDigits = schema.maxFractionDigits;
  return out;
}

/** Returns the list of violated keywords, independently of the main engine. */
export function referenceViolations(compiled: CompiledReference, valueText: string): string[] {
  let v: Rat;
  try {
    v = parseRat(valueText);
  } catch {
    return ["decimal"];
  }
  const bad: string[] = [];
  if (compiled.minimum && cmpRat(v, compiled.minimum) < 0) bad.push("minimum");
  if (compiled.maximum && cmpRat(v, compiled.maximum) > 0) bad.push("maximum");
  if (compiled.exclusiveMinimum && cmpRat(v, compiled.exclusiveMinimum) <= 0) bad.push("exclusiveMinimum");
  if (compiled.exclusiveMaximum && cmpRat(v, compiled.exclusiveMaximum) >= 0) bad.push("exclusiveMaximum");
  if (compiled.multipleOf && !ratIsMultiple(v, compiled.multipleOf)) bad.push("multipleOf");
  if (compiled.maxTotalDigits !== undefined && ratTotalDigits(v) > compiled.maxTotalDigits) {
    bad.push("maxTotalDigits");
  }
  if (compiled.maxFractionDigits !== undefined && ratFractionDigits(v) > compiled.maxFractionDigits) {
    bad.push("maxFractionDigits");
  }
  return bad;
}

/**
 * Finite scaled-integer enumeration: every value k * 10^-scale with
 * |k| <= limit that the reference accepts.
 */
export function enumerateAccepted(compiled: CompiledReference, scale: number, limit: number): string[] {
  const accepted: string[] = [];
  for (let k = -limit; k <= limit; k += 1) {
    const text = `${k}e-${scale}`;
    if (referenceViolations(compiled, text).length === 0) accepted.push(text);
  }
  return accepted;
}
