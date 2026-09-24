/**
 * UTF-16 offset utilities.
 *
 * All public offsets in this library are UTF-16 code unit offsets (the same
 * unit used by JavaScript string indices and the LSP `character` field).
 */

export interface Position {
  /** Zero-based line index. */
  line: number;
  /** Zero-based UTF-16 code unit offset within the line. */
  character: number;
}

export class OffsetError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'OffsetError';
  }
}

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/**
 * Returns true when `offset` falls between a high and a low surrogate,
 * i.e. slicing there would split a surrogate pair.
 */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  if (offset <= 0 || offset >= text.length) return false;
  return (
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/** Validate that `offset` is in bounds and does not split a surrogate pair. */
export function assertOffset(text: string, offset: number, name = 'offset'): void {
  if (!Number.isInteger(offset)) {
    throw new OffsetError(`${name} must be an integer, got ${offset}`);
  }
  if (offset < 0 || offset > text.length) {
    throw new OffsetError(
      `${name} ${offset} is out of bounds [0, ${text.length}]`,
    );
  }
  if (splitsSurrogatePair(text, offset)) {
    throw new OffsetError(
      `${name} ${offset} splits a surrogate pair (UTF-16 offsets must not divide an astral character)`,
    );
  }
}

/** Validate a half-open range [start, end) against the text. */
export function assertRange(
  text: string,
  start: number,
  end: number,
  name = 'range',
): void {
  assertOffset(text, start, `${name}.start`);
  assertOffset(text, end, `${name}.end`);
  if (start > end) {
    throw new OffsetError(`${name} has start ${start} > end ${end}`);
  }
}

/**
 * Compute the offset of the first code unit of every line.
 * Handles LF, CRLF and lone CR line terminators.
 */
export function computeLineStarts(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code === 0x0d /* \r */) {
      if (i + 1 < text.length && text.charCodeAt(i + 1) === 0x0a /* \n */) i++;
      starts.push(i + 1);
    } else if (code === 0x0a /* \n */) {
      starts.push(i + 1);
    }
  }
  return starts;
}

/** Convert a UTF-16 offset to a (line, character) position. */
export function offsetToPosition(text: string, offset: number): Position {
  assertOffset(text, offset);
  const starts = computeLineStarts(text);
  let low = 0;
  let high = starts.length;
  while (low + 1 < high) {
    const mid = (low + high) >> 1;
    if (starts[mid] <= offset) low = mid;
    else high = mid;
  }
  return { line: low, character: offset - starts[low] };
}

/**
 * Convert a (line, character) position to a UTF-16 offset.
 * The character is clamped to the end of the line content (before any
 * line terminator) and never lands between CR and LF or inside a
 * surrogate pair.
 */
export function positionToOffset(text: string, position: Position): number {
  const starts = computeLineStarts(text);
  if (position.line < 0 || position.line >= starts.length) {
    throw new OffsetError(
      `line ${position.line} is out of bounds [0, ${starts.length - 1}]`,
    );
  }
  const lineStart = starts[position.line];
  // Find the end of the line content (offset of the line terminator, if any).
  let contentEnd = lineStart;
  while (
    contentEnd < text.length &&
    text.charCodeAt(contentEnd) !== 0x0a &&
    text.charCodeAt(contentEnd) !== 0x0d
  ) {
    contentEnd++;
  }
  let offset = Math.min(lineStart + Math.max(0, position.character), contentEnd);
  // Never split a surrogate pair: back off to the pair boundary.
  if (splitsSurrogatePair(text, offset)) offset--;
  return offset;
}
