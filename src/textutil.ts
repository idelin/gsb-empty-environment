export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/**
 * Returns true when `offset` falls between a high and a low surrogate in
 * `text`, i.e. cutting the text there would split a surrogate pair.
 * Offsets are UTF-16 code-unit indices (JavaScript string indices).
 */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/**
 * Clamps `offset` into [0, text.length] and nudges it to the start of a
 * surrogate pair when it would otherwise land in the middle of one.
 */
export function clampToSurrogateBoundary(text: string, offset: number): number {
  const clamped = Math.max(0, Math.min(offset, text.length));
  return splitsSurrogatePair(text, clamped) ? clamped - 1 : clamped;
}
