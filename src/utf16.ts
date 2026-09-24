/**
 * UTF-16 offset helpers.
 *
 * All public offsets in this library are UTF-16 code-unit offsets (i.e. plain
 * JavaScript string indices). A "safe" offset never falls between the high and
 * low surrogate of a surrogate pair, so projections and edits can never cut a
 * grapheme such as an emoji in half.
 */

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` does not split a surrogate pair in `text`. */
export function isSafeOffset(text: string, offset: number): boolean {
  if (offset <= 0 || offset >= text.length) return true;
  return !(
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/**
 * Snap `offset` to the nearest safe offset. `side` controls the direction:
 *  - "start": move left (before the pair) — used for range starts.
 *  - "end":   move right (after the pair) — used for range ends.
 */
export function snapOffset(
  text: string,
  offset: number,
  side: "start" | "end",
): number {
  const clamped = Math.max(0, Math.min(text.length, offset));
  if (isSafeOffset(text, clamped)) return clamped;
  return side === "start" ? clamped - 1 : clamped + 1;
}

/** Snap a half-open range outward so neither boundary splits a surrogate pair. */
export function snapRangeOutward(
  text: string,
  start: number,
  end: number,
): [number, number] {
  return [snapOffset(text, start, "start"), snapOffset(text, end, "end")];
}

export interface Position {
  line: number; // 0-based
  character: number; // 0-based UTF-16 code units within the line
}

/**
 * Convert a UTF-16 offset to a line/character position.
 * Handles LF, CRLF and lone CR line terminators; CRLF counts as one break.
 */
export function positionAt(text: string, offset: number): Position {
  const clamped = Math.max(0, Math.min(text.length, offset));
  let line = 0;
  let lineStart = 0;
  let i = 0;
  while (i < clamped) {
    const ch = text.charCodeAt(i);
    if (ch === 0x0d /* \r */) {
      if (i + 1 < clamped && text.charCodeAt(i + 1) === 0x0a /* \n */) i++;
      line++;
      lineStart = i + 1;
    } else if (ch === 0x0a /* \n */) {
      line++;
      lineStart = i + 1;
    }
    i++;
  }
  return { line, character: clamped - lineStart };
}

/**
 * Convert a line/character position to a UTF-16 offset.
 * Character offsets are clamped to the line's content end (before its
 * terminator) and never split a surrogate pair.
 */
export function offsetAt(text: string, position: Position): number {
  let line = 0;
  let lineStart = 0;
  let i = 0;
  while (i < text.length && line < position.line) {
    const ch = text.charCodeAt(i);
    if (ch === 0x0d) {
      if (i + 1 < text.length && text.charCodeAt(i + 1) === 0x0a) i++;
      line++;
      lineStart = i + 1;
    } else if (ch === 0x0a) {
      line++;
      lineStart = i + 1;
    }
    i++;
  }
  if (line < position.line) return text.length;
  // Find end of line content (start of terminator).
  let lineEnd = text.length;
  for (let j = lineStart; j < text.length; j++) {
    const ch = text.charCodeAt(j);
    if (ch === 0x0d || ch === 0x0a) {
      lineEnd = j;
      break;
    }
  }
  const raw = Math.min(lineStart + Math.max(0, position.character), lineEnd);
  return snapOffset(text, raw, "start");
}
