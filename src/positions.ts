/**
 * Conversion between (line, character) positions and UTF-16 offsets.
 * Both LF (`\n`) and CRLF (`\r\n`) line endings are recognized; a lone
 * `\r` is treated as a regular character.
 */

import { assertUtf16Boundary } from './utf16.ts';

export interface Position {
  line: number;
  character: number;
}

/** Returns the UTF-16 offset of `position` in `text`. */
export function offsetAtPosition(text: string, position: Position): number {
  const { line, character } = position;
  if (!Number.isInteger(line) || line < 0 || !Number.isInteger(character) || character < 0) {
    throw new RangeError(`Invalid position ${JSON.stringify(position)}`);
  }
  let currentLine = 0;
  let lineStart = 0;
  let i = 0;
  while (i < text.length && currentLine < line) {
    const ch = text[i];
    if (ch === '\r' && text[i + 1] === '\n') {
      i += 2;
    } else if (ch === '\n') {
      i += 1;
    } else {
      i += 1;
      continue;
    }
    currentLine += 1;
    lineStart = i;
  }
  if (currentLine < line) {
    throw new RangeError(`Line ${line} is out of range`);
  }
  const offset = lineStart + character;
  // The character must not land past the end of the requested line.
  const lineEnd = lineEndOffset(text, lineStart);
  if (offset > lineEnd) {
    throw new RangeError(`Character ${character} is out of range for line ${line}`);
  }
  assertUtf16Boundary(text, offset, 'Position offset');
  return offset;
}

/** Returns the (line, character) position of a UTF-16 `offset` in `text`. */
export function positionAtOffset(text: string, offset: number): Position {
  assertUtf16Boundary(text, offset, 'Offset');
  let line = 0;
  let lineStart = 0;
  let i = 0;
  while (i < offset) {
    const ch = text[i];
    if (ch === '\r' && text[i + 1] === '\n') {
      if (i + 2 > offset) break; // offset points at or inside this CRLF
      i += 2;
      line += 1;
      lineStart = i;
    } else if (ch === '\n') {
      i += 1;
      line += 1;
      lineStart = i;
    } else {
      i += 1;
    }
  }
  return { line, character: Math.min(offset, i) - lineStart };
}

function lineEndOffset(text: string, lineStart: number): number {
  let i = lineStart;
  while (i < text.length && text[i] !== '\n' && text[i] !== '\r') {
    i += 1;
  }
  return i;
}
