/**
 * All public offsets are UTF-16 code-unit offsets (the same unit as
 * JavaScript string indices and `String.prototype.length`). These
 * helpers guarantee an offset never lands inside a surrogate pair.
 */

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` sits between a high and a low surrogate. */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/** Move one code unit back if the offset would split a surrogate pair. */
export function snapOffsetBackward(text: string, offset: number): number {
  return splitsSurrogatePair(text, offset) ? offset - 1 : offset;
}

/** Move one code unit forward if the offset would split a surrogate pair. */
export function snapOffsetForward(text: string, offset: number): number {
  return splitsSurrogatePair(text, offset) ? offset + 1 : offset;
}

/** Truncate to an integer and clamp into [0, text.length]. */
export function clampOffset(text: string, offset: number): number {
  const o = Math.trunc(offset);
  if (o < 0) return 0;
  if (o > text.length) return text.length;
  return o;
}
