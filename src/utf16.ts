/**
 * UTF-16 offset utilities.
 *
 * All public offsets in this project are UTF-16 code-unit offsets (the same
 * unit JavaScript strings and browser text APIs use). A lone `\r\n` counts as
 * two offsets. Surrogate pairs must never be split: any offset that lands
 * between a high and a low surrogate is snapped to a pair boundary.
 */

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

export function clampOffset(text: string, offset: number): number {
  if (offset < 0) return 0;
  if (offset > text.length) return text.length;
  return offset;
}

/** True when `offset` falls strictly inside a surrogate pair. */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  if (offset <= 0 || offset >= text.length) return false;
  return (
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/**
 * Snap an insertion point so it never splits a surrogate pair.
 * Deterministic rule: a point inside a pair moves LEFT (before the pair).
 */
export function snapPoint(text: string, offset: number): number {
  const clamped = clampOffset(text, offset);
  return splitsSurrogatePair(text, clamped) ? clamped - 1 : clamped;
}

export interface SnapRange {
  start: number;
  end: number;
}

/**
 * Expand a range outward so neither boundary splits a surrogate pair:
 * the start snaps left, the end snaps right. The result is clamped and
 * guaranteed to satisfy
 *
 * A collapsed (insertion) range snaps as a single point instead, so an
 * insertion never turns into a deletion of the pair it landed in. `0 <= start <= end <= text.length`.
 */
export function snapRange(text: string, start: number, end: number): SnapRange {
  if (start === end) {
    const point = snapPoint(text, start);
    return { start: point, end: point };
  }
  const snappedStart = snapPoint(text, start);
  let snappedEnd = clampOffset(text, Math.max(end, snappedStart));
  if (snappedEnd < snappedStart) snappedEnd = snappedStart;
  if (splitsSurrogatePair(text, snappedEnd)) snappedEnd += 1;
  return { start: snappedStart, end: snappedEnd };
}

/** True when the string contains an unpaired (lone) surrogate code unit. */
export function hasLoneSurrogates(text: string): boolean {
  for (let i = 0; i < text.length; i += 1) {
    const code = text.charCodeAt(i);
    if (isHighSurrogate(code)) {
      if (i + 1 >= text.length || !isLowSurrogate(text.charCodeAt(i + 1))) {
        return true;
      }
      i += 1;
    } else if (isLowSurrogate(code)) {
      return true;
    }
  }
  return false;
}

/** Number of Unicode code points (surrogate pair counts as one). */
export function codePointLength(text: string): number {
  let count = 0;
  for (let i = 0; i < text.length; i += 1) {
    count += 1;
    if (
      isHighSurrogate(text.charCodeAt(i)) &&
      i + 1 < text.length &&
      isLowSurrogate(text.charCodeAt(i + 1))
    ) {
      i += 1;
    }
  }
  return count;
}
