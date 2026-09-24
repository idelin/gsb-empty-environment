/**
 * Public offsets are UTF-16 code unit indices (i.e. plain JS string indices).
 *
 * Surrogate-pair rule (public): any offset that would split a surrogate pair
 * is snapped DOWN to the start of the pair, so a pair is never cut.
 */

export function isHighSurrogate(codeUnit: number): boolean {
  return codeUnit >= 0xd800 && codeUnit <= 0xdbff;
}

export function isLowSurrogate(codeUnit: number): boolean {
  return codeUnit >= 0xdc00 && codeUnit <= 0xdfff;
}

/** Clamp into [0, text.length] and snap a mid-pair offset down to the pair start. */
export function clampOffset(text: string, offset: number): number {
  let o = Math.trunc(offset);
  if (Number.isNaN(o)) o = 0;
  o = Math.max(0, Math.min(text.length, o));
  if (
    o > 0 &&
    o < text.length &&
    isLowSurrogate(text.charCodeAt(o)) &&
    isHighSurrogate(text.charCodeAt(o - 1))
  ) {
    o -= 1;
  }
  return o;
}

/** Clamp a range; guarantees 0 <= start <= end <= text.length and no split pairs. */
export function clampRange(
  text: string,
  start: number,
  end: number,
): { start: number; end: number } {
  const s = clampOffset(text, Math.min(start, end));
  const e = clampOffset(text, Math.max(start, end));
  return { start: s, end: Math.max(s, e) };
}
