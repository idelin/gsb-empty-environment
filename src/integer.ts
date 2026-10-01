/**
 * Chunked big-integer <-> digit-string conversion.
 *
 * Why chunking (the "no unexplained degradation" rule):
 *  Naively emitting digits divides the whole bignum by the SMALL base once
 *  per output digit: n digits cost n full-length divisions, and since each
 *  division is O(length), total work degrades to O(n^2) in the digit count.
 *  Instead we divide once per CHUNK of k digits by base^k (a single large
 *  divisor), cutting the number of bignum divisions by a factor of k.
 *  Parsing is symmetric: we accumulate k-digit chunks with one
 *  multiply-add per chunk instead of per digit.
 *  k is chosen as the largest value with base^k <= 2^53 so each chunk value
 *  also stays exactly representable as a double for cheap per-chunk handling;
 *  all bignum arithmetic itself is BigInt-only.
 *
 * Workload accounting: every bignum division (output) and every bignum
 * multiply-add (input) is counted and surfaced in the conversion report.
 */

import { buildLookup, resolveCharset } from "./charset.ts";

export interface Workload {
  divisions: number;
  multiplyAdds: number;
}

export function chunkSizeFor(base: number): number {
  let k = 1;
  let p = BigInt(base);
  const LIMIT = 1n << 53n;
  const b = BigInt(base);
  while (p * b <= LIMIT) {
    p *= b;
    k++;
  }
  return k;
}

export class InvalidDigitError extends Error {
  readonly position: number;
  readonly char: string;
  readonly base: number;
  constructor(position: number, char: string, base: number) {
    super(`invalid digit '${char}' at position ${position} for base ${base}`);
    this.name = "InvalidDigitError";
    this.position = position;
    this.char = char;
    this.base = base;
  }
}

/** Convert a non-negative BigInt to a digit string. Counts divisions. */
export function intToDigits(
  value: bigint,
  base: number,
  charset: string,
  workload?: Workload,
): string {
  const cs = resolveCharset(charset, base);
  if (value < 0n) throw new RangeError("intToDigits expects a non-negative value");
  if (value === 0n) return cs[0];

  const k = chunkSizeFor(base);
  const chunkBase = BigInt(base) ** BigInt(k);
  const chunks: number[] = [];
  let rest = value;
  while (rest > 0n) {
    const q = rest / chunkBase;
    const r = rest - q * chunkBase;
    chunks.push(Number(r)); // r < base^k <= 2^53, exact
    rest = q;
    if (workload) workload.divisions++;
  }

  let out = "";
  for (let i = chunks.length - 1; i >= 0; i--) {
    let chunk = chunks[i];
    // Most-significant chunk is emitted without leading zeros.
    if (i === chunks.length - 1) {
      let s = "";
      while (chunk > 0) {
        s = cs[chunk % base] + s;
        chunk = Math.floor(chunk / base);
      }
      out += s === "" ? cs[0] : s;
    } else {
      let s = "";
      for (let j = 0; j < k; j++) {
        s = cs[chunk % base] + s;
        chunk = Math.floor(chunk / base);
      }
      out += s;
    }
  }
  return out;
}

/**
 * Parse a digit string (integer part only, no sign) to BigInt.
 * Throws InvalidDigitError with the exact position of the first bad digit.
 */
export function digitsToInt(
  digits: string,
  base: number,
  charset: string,
  workload?: Workload,
  positionOffset = 0,
): bigint {
  const cs = resolveCharset(charset, base);
  const lookup = buildLookup(cs, base);
  if (digits.length === 0) throw new InvalidDigitError(positionOffset, "", base);

  const k = chunkSizeFor(base);
  let acc = 0n;
  for (let start = 0; start < digits.length; start += k) {
    const end = Math.min(start + k, digits.length);
    let chunkVal = 0;
    let chunkPow = 1;
    for (let i = start; i < end; i++) {
      const ch = digits[i];
      const v = lookup.get(ch);
      if (v === undefined) throw new InvalidDigitError(positionOffset + i, ch, base);
      chunkVal = chunkVal * base + v;
      chunkPow *= base;
    }
    acc = acc * BigInt(chunkPow) + BigInt(chunkVal);
    if (workload) workload.multiplyAdds++;
  }
  return acc;
}
