/**
 * UTF-16 offset helpers.
 *
 * All public offsets/lengths in this library are UTF-16 code units, which
 * matches JavaScript string indexing (`string.length`, `string.slice`).
 * A boundary is valid only if it does not split a surrogate pair.
 */

export function utf16Length(text: string): number {
  return text.length;
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/**
 * True when `offset` falls between a high and a low surrogate, i.e. cutting
 * `text` at `offset` would split a surrogate pair.
 */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  if (offset <= 0 || offset >= text.length) return false;
  return (
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/** True when both cuts of [start, end) keep surrogate pairs intact. */
export function isPairSafeRange(text: string, start: number, end: number): boolean {
  return !splitsSurrogatePair(text, start) && !splitsSurrogatePair(text, end);
}

/** True when the string contains no lone surrogates. */
export function isWellFormed(text: string): boolean {
  return text.isWellFormed();
}
