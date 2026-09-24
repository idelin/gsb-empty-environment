/**
 * All public offsets in this project are UTF-16 code-unit offsets
 * (i.e. JavaScript string indices). Surrogate pairs must never be split.
 */

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` falls between the two halves of a surrogate pair. */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  if (offset <= 0 || offset >= text.length) return false;
  return (
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/** Throw when an edit boundary would cut a surrogate pair in two. */
export function assertUtf16Boundary(
  text: string,
  offset: number,
  what: string,
): void {
  if (offset < 0 || offset > text.length) {
    throw new RangeError(
      `${what} at UTF-16 offset ${offset} is out of range [0, ${text.length}]`,
    );
  }
  if (splitsSurrogatePair(text, offset)) {
    throw new RangeError(
      `${what} at UTF-16 offset ${offset} splits a surrogate pair`,
    );
  }
}

/** Snap an offset back to the nearest offset that does not split a pair. */
export function snapBackward(text: string, offset: number): number {
  return splitsSurrogatePair(text, offset) ? offset - 1 : offset;
}

/** Snap an offset forward to the nearest offset that does not split a pair. */
export function snapForward(text: string, offset: number): number {
  return splitsSurrogatePair(text, offset) ? offset + 1 : offset;
}
