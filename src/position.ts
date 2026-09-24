/**
 * Line/character <-> UTF-16 offset conversion.
 *
 * Lines are terminated by LF, CRLF or a lone CR. `character` is a UTF-16
 * code-unit offset within the line, matching LSP/VS Code conventions.
 */

export interface Position {
  line: number;
  character: number;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/** Convert a UTF-16 offset into a zero-based line/character position. */
export function positionAt(text: string, offset: number): Position {
  const target = clamp(Math.round(offset), 0, text.length);
  let line = 0;
  let lineStart = 0;
  let i = 0;
  while (i < target) {
    const code = text.charCodeAt(i);
    if (code === 13 /* \r */) {
      if (i + 1 < text.length && text.charCodeAt(i + 1) === 10 /* \n */) {
        if (target === i + 1) break; // offset sits between \r and \n: end of line
        i += 2;
      } else {
        i += 1;
      }
      line++;
      lineStart = i;
    } else if (code === 10 /* \n */) {
      i += 1;
      line++;
      lineStart = i;
    } else {
      i += 1;
    }
  }
  return { line, character: target - lineStart };
}

/** Convert a zero-based line/character position into a UTF-16 offset. */
export function offsetAt(text: string, position: Position): number {
  const targetLine = Math.max(0, Math.trunc(position.line));
  let line = 0;
  let lineStart = 0;
  let i = 0;
  while (i < text.length && line < targetLine) {
    const code = text.charCodeAt(i);
    if (code === 13 /* \r */) {
      i += i + 1 < text.length && text.charCodeAt(i + 1) === 10 ? 2 : 1;
      line++;
      lineStart = i;
    } else if (code === 10 /* \n */) {
      i += 1;
      line++;
      lineStart = i;
    } else {
      i += 1;
    }
  }
  if (line < targetLine) return text.length; // line beyond EOF clamps to EOF
  // Find the end of the current line (before its terminator).
  let lineEnd = text.length;
  for (let j = lineStart; j < text.length; j++) {
    const code = text.charCodeAt(j);
    if (code === 13 || code === 10) {
      lineEnd = j;
      break;
    }
  }
  return clamp(lineStart + Math.max(0, Math.trunc(position.character)), lineStart, lineEnd);
}
