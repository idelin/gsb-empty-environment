/**
 * UTF-16 code-unit helpers.
 *
 * All public offsets in this library are UTF-16 code-unit offsets, which
 * matches JavaScript string indexing (`string.length`, `charCodeAt`).
 * An offset is only valid if it does not fall inside a surrogate pair.
 */

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** Number of UTF-16 code units in `text`. */
export function utf16Length(text: string): number {
  return text.length;
}

/**
 * Returns true when `offset` is a legal UTF-16 boundary in `text`,
 * i.e. it does not split a surrogate pair. `0` and `text.length` are
 * always valid boundaries.
 */
export function isUtf16Boundary(text: string, offset: number): boolean {
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) {
    return false;
  }
  if (offset === 0 || offset === text.length) {
    return true;
  }
  const before = text.charCodeAt(offset - 1);
  const after = text.charCodeAt(offset);
  return !(isHighSurrogate(before) && isLowSurrogate(after));
}

/** Throws when `offset` splits a surrogate pair or is out of range. */
export function assertUtf16Boundary(text: string, offset: number, what: string): void {
  if (!isUtf16Boundary(text, offset)) {
    throw new RangeError(
      `${what} (${offset}) is not a valid UTF-16 boundary ` +
        `(out of range or splits a surrogate pair)`,
    );
  }
}
