/**
 * Text model: line splitting and UTF-16 offset utilities.
 *
 * All public offsets are UTF-16 code-unit offsets (i.e. plain JS string
 * indices). Both LF and CRLF line endings are recognized; a lone CR is
 * treated as a regular character.
 */

export type Eol = "\n" | "\r\n" | "";

export interface LineInfo {
  /** Line content without the line terminator. */
  readonly text: string;
  /** The line terminator that follows the content ("" for the last line without one). */
  readonly eol: Eol;
  /** UTF-16 offset of the first character of the line. */
  readonly start: number;
  /** UTF-16 offset just past the line content (before the terminator). */
  readonly contentEnd: number;
  /** UTF-16 offset just past the terminator (== start of the next line). */
  readonly end: number;
}

const LF = 0x0a;
const CR = 0x0d;

/**
 * Splits `text` into lines. A trailing terminator does NOT produce an extra
 * empty line ("a\n" is one line). Empty text yields zero lines.
 */
export function splitLines(text: string): LineInfo[] {
  const lines: LineInfo[] = [];
  const n = text.length;
  let start = 0;
  let i = 0;
  while (i < n) {
    const code = text.charCodeAt(i);
    if (code === LF) {
      lines.push({ text: text.slice(start, i), eol: "\n", start, contentEnd: i, end: i + 1 });
      i += 1;
      start = i;
    } else if (code === CR && i + 1 < n && text.charCodeAt(i + 1) === LF) {
      lines.push({ text: text.slice(start, i), eol: "\r\n", start, contentEnd: i, end: i + 2 });
      i += 2;
      start = i;
    } else {
      i += 1;
    }
  }
  if (start < n) {
    lines.push({ text: text.slice(start), eol: "", start, contentEnd: n, end: n });
  }
  return lines;
}

/**
 * Returns true when `offset` is a legal cut point in `text`, i.e. it does not
 * fall between a high surrogate and its low surrogate.
 */
export function isSurrogateBoundary(text: string, offset: number): boolean {
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) return false;
  if (offset === 0 || offset === text.length) return true;
  const before = text.charCodeAt(offset - 1);
  const after = text.charCodeAt(offset);
  const highBefore = before >= 0xd800 && before <= 0xdbff;
  const lowAfter = after >= 0xdc00 && after <= 0xdfff;
  return !(highBefore && lowAfter);
}

/**
 * Throws when `offset` is out of range or would split a surrogate pair.
 */
export function assertSafeOffset(text: string, offset: number): void {
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) {
    throw new RangeError(`offset ${offset} is out of range [0, ${text.length}]`);
  }
  if (!isSurrogateBoundary(text, offset)) {
    throw new RangeError(`offset ${offset} splits a UTF-16 surrogate pair`);
  }
}
