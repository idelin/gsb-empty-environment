/**
 * Explicit, case-sensitive charsets for bases 2..62.
 *
 * Rules:
 *  - A charset is an explicit ordered string of unique digit characters.
 *  - Matching is case-sensitive: 'a' and 'A' are different digits and may
 *    coexist (e.g. BASE62), but the SAME character may not appear twice.
 *  - Reserved characters may not be digits: '-' '+' '.' '(' ')'
 *    (they carry syntactic meaning in the numeral grammar).
 */

export const DIGITS_LOWER = "0123456789abcdefghijklmnopqrstuvwxyz"; // base 2..36
export const DIGITS_UPPER = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"; // base 2..36
export const DIGITS_BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"; // base 2..62
export const DIGITS_BASE62_ALT = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"; // lower-first variant

export const CHARSETS: Record<string, string> = {
  lower: DIGITS_LOWER,
  upper: DIGITS_UPPER,
  base62: DIGITS_BASE62,
  base62alt: DIGITS_BASE62_ALT,
};

export const MIN_BASE = 2;
export const MAX_BASE = 62;

const RESERVED = new Set(["-", "+", ".", "(", ")"]);

export class CharsetError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CharsetError";
  }
}

/** Validate an explicit charset for a given base. Returns the charset. */
export function validateCharset(charset: string, base: number): string {
  if (!Number.isInteger(base) || base < MIN_BASE || base > MAX_BASE) {
    throw new CharsetError(`base must be an integer in [${MIN_BASE}, ${MAX_BASE}], got ${base}`);
  }
  if (typeof charset !== "string" || charset.length < base) {
    throw new CharsetError(
      `charset for base ${base} must provide at least ${base} characters, got ${charset?.length ?? 0}`,
    );
  }
  // Uniqueness is checked over the WHOLE provided string (not just the
  // used prefix) so typos in over-long explicit charsets are caught.
  const seen = new Set<string>();
  for (let i = 0; i < charset.length; i++) {
    const ch = charset[i];
    if (RESERVED.has(ch)) {
      throw new CharsetError(`charset character '${ch}' at index ${i} is reserved syntax`);
    }
    if (seen.has(ch)) {
      throw new CharsetError(`charset character '${ch}' at index ${i} is duplicated (case-sensitive check)`);
    }
    seen.add(ch);
  }
  return charset;
}

/** Resolve a charset from an explicit string or a named preset. */
export function resolveCharset(charset: string, base: number): string {
  const named = CHARSETS[charset];
  const resolved = named !== undefined && named.length >= base ? named : charset;
  return validateCharset(resolved, base);
}

/** Build a case-sensitive reverse lookup: digit char -> value. */
export function buildLookup(charset: string, base: number): Map<string, number> {
  const map = new Map<string, number>();
  for (let i = 0; i < base; i++) map.set(charset[i], i);
  return map;
}
