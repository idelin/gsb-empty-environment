/**
 * UTF-8 utilities.
 *
 * All offsets in this library are counted in Unicode scalar values
 * (code points). A supplementary-plane character (UTF-16 surrogate pair,
 * 4 bytes in UTF-8) counts as exactly one scalar and can never be split.
 */

export class InvalidEncodingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidEncodingError";
    Object.setPrototypeOf(this, InvalidEncodingError.prototype);
  }
}

function isHighSurrogate(u: number): boolean {
  return u >= 0xd800 && u <= 0xdbff;
}

function isLowSurrogate(u: number): boolean {
  return u >= 0xdc00 && u <= 0xdfff;
}

function isContinuation(b: number): boolean {
  return b >= 0x80 && b <= 0xbf;
}

/** Rejects strings containing lone surrogates (not encodable as UTF-8). */
export function validateString(s: string): void {
  for (let i = 0; i < s.length; i++) {
    const u = s.charCodeAt(i);
    if (isHighSurrogate(u)) {
      if (i + 1 >= s.length || !isLowSurrogate(s.charCodeAt(i + 1))) {
        throw new InvalidEncodingError("lone high surrogate at UTF-16 index " + i);
      }
      i++;
    } else if (isLowSurrogate(u)) {
      throw new InvalidEncodingError("lone low surrogate at UTF-16 index " + i);
    }
  }
}

/**
 * Strict UTF-8 validation (RFC 3629): rejects overlong forms, surrogate
 * code points, code points above U+10FFFF and truncated sequences.
 */
export function isValidUtf8(bytes: Uint8Array): boolean {
  const n = bytes.length;
  let i = 0;
  while (i < n) {
    const b0 = bytes[i];
    if (b0 <= 0x7f) {
      i += 1;
    } else if (b0 >= 0xc2 && b0 <= 0xdf) {
      if (i + 1 >= n || !isContinuation(bytes[i + 1])) return false;
      i += 2;
    } else if (b0 === 0xe0) {
      if (i + 2 >= n) return false;
      const b1 = bytes[i + 1];
      if (b1 < 0xa0 || b1 > 0xbf || !isContinuation(bytes[i + 2])) return false;
      i += 3;
    } else if ((b0 >= 0xe1 && b0 <= 0xec) || (b0 >= 0xee && b0 <= 0xef)) {
      if (i + 2 >= n) return false;
      if (!isContinuation(bytes[i + 1]) || !isContinuation(bytes[i + 2])) return false;
      i += 3;
    } else if (b0 === 0xed) {
      if (i + 2 >= n) return false;
      const b1 = bytes[i + 1];
      // U+D800..U+DFFF are surrogates and invalid in UTF-8.
      if (b1 < 0x80 || b1 > 0x9f || !isContinuation(bytes[i + 2])) return false;
      i += 3;
    } else if (b0 === 0xf0) {
      if (i + 3 >= n) return false;
      const b1 = bytes[i + 1];
      if (b1 < 0x90 || b1 > 0xbf) return false;
      if (!isContinuation(bytes[i + 2]) || !isContinuation(bytes[i + 3])) return false;
      i += 4;
    } else if (b0 >= 0xf1 && b0 <= 0xf3) {
      if (i + 3 >= n) return false;
      if (!isContinuation(bytes[i + 1]) || !isContinuation(bytes[i + 2]) || !isContinuation(bytes[i + 3])) return false;
      i += 4;
    } else if (b0 === 0xf4) {
      if (i + 3 >= n) return false;
      const b1 = bytes[i + 1];
      // U+100000..U+10FFFF only.
      if (b1 < 0x80 || b1 > 0x8f) return false;
      if (!isContinuation(bytes[i + 2]) || !isContinuation(bytes[i + 3])) return false;
      i += 4;
    } else {
      // 0x80..0xbf stray continuation, 0xc0/0xc1 overlong, 0xf5..0xff out of range.
      return false;
    }
  }
  return true;
}

/** Decodes strictly validated UTF-8 bytes into a string. Throws on invalid input. */
export function decodeUtf8(bytes: Uint8Array): string {
  if (!isValidUtf8(bytes)) {
    throw new InvalidEncodingError("input is not valid UTF-8");
  }
  let out = "";
  let i = 0;
  const n = bytes.length;
  while (i < n) {
    const b0 = bytes[i];
    let cp: number;
    let len: number;
    if (b0 <= 0x7f) {
      cp = b0;
      len = 1;
    } else if (b0 <= 0xdf) {
      cp = b0 & 0x1f;
      len = 2;
    } else if (b0 <= 0xef) {
      cp = b0 & 0x0f;
      len = 3;
    } else {
      cp = b0 & 0x07;
      len = 4;
    }
    for (let j = 1; j < len; j++) {
      cp = (cp << 6) | (bytes[i + j] & 0x3f);
    }
    out += String.fromCodePoint(cp);
    i += len;
  }
  return out;
}

/** Encodes a (valid) string to UTF-8 bytes. */
export function encodeUtf8(s: string): Uint8Array {
  const out = new Uint8Array(utf8ByteLength(s));
  let o = 0;
  for (let i = 0; i < s.length; i++) {
    let cp = s.charCodeAt(i);
    if (isHighSurrogate(cp) && i + 1 < s.length) {
      const lo = s.charCodeAt(i + 1);
      if (isLowSurrogate(lo)) {
        cp = 0x10000 + ((cp - 0xd800) << 10) + (lo - 0xdc00);
        i++;
      }
    }
    if (cp < 0x80) {
      out[o++] = cp;
    } else if (cp < 0x800) {
      out[o++] = 0xc0 | (cp >> 6);
      out[o++] = 0x80 | (cp & 0x3f);
    } else if (cp < 0x10000) {
      out[o++] = 0xe0 | (cp >> 12);
      out[o++] = 0x80 | ((cp >> 6) & 0x3f);
      out[o++] = 0x80 | (cp & 0x3f);
    } else {
      out[o++] = 0xf0 | (cp >> 18);
      out[o++] = 0x80 | ((cp >> 12) & 0x3f);
      out[o++] = 0x80 | ((cp >> 6) & 0x3f);
      out[o++] = 0x80 | (cp & 0x3f);
    }
  }
  return out;
}

/** Number of Unicode scalar values (code points) in a valid string. */
export function countScalars(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const u = s.charCodeAt(i);
    if (isHighSurrogate(u) && i + 1 < s.length && isLowSurrogate(s.charCodeAt(i + 1))) {
      i++;
    }
    n++;
  }
  return n;
}

/** Number of bytes the string occupies when encoded as UTF-8. */
export function utf8ByteLength(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const u = s.charCodeAt(i);
    if (u < 0x80) {
      n += 1;
    } else if (u < 0x800) {
      n += 2;
    } else if (isHighSurrogate(u) && i + 1 < s.length && isLowSurrogate(s.charCodeAt(i + 1))) {
      n += 4;
      i++;
    } else {
      n += 3;
    }
  }
  return n;
}

/**
 * Returns the substring covering scalar offsets [start, end).
 * Never splits a surrogate pair: boundaries are scalar boundaries.
 * Requires 0 <= start <= end <= countScalars(s).
 */
export function sliceScalars(s: string, start: number, end: number): string {
  let lo = -1;
  let hi = -1;
  let k = 0;
  let i = 0;
  for (;;) {
    if (k === start && lo < 0) lo = i;
    if (k === end) {
      hi = i;
      break;
    }
    const u = s.charCodeAt(i);
    if (isHighSurrogate(u) && i + 1 < s.length && isLowSurrogate(s.charCodeAt(i + 1))) {
      i += 2;
    } else {
      i += 1;
    }
    k++;
  }
  if (lo < 0) lo = hi;
  return s.slice(lo, hi);
}
