/**
 * Line model. A text is split into lines; every line keeps its own
 * terminator so LF, CRLF and lone CR can be mixed freely and the
 * original text can always be reconstructed byte-for-byte.
 */

export type Eol = 'LF' | 'CRLF' | 'CR' | 'NONE';

export interface Line {
  /** Line content without the terminator. */
  readonly text: string;
  /** Terminator that follows the content ('NONE' for a trailing line). */
  readonly eol: Eol;
  /** UTF-16 offset of the first code unit of this line in the source text. */
  readonly start: number;
}

const CR = 13;
const LF = 10;

export function splitLines(text: string): Line[] {
  const lines: Line[] = [];
  const n = text.length;
  let start = 0;
  let i = 0;
  while (i < n) {
    const code = text.charCodeAt(i);
    if (code === CR || code === LF) {
      const eolStart = i;
      let eol: Eol;
      if (code === CR && i + 1 < n && text.charCodeAt(i + 1) === LF) {
        eol = 'CRLF';
        i += 2;
      } else if (code === CR) {
        eol = 'CR';
        i += 1;
      } else {
        eol = 'LF';
        i += 1;
      }
      lines.push({ text: text.slice(start, eolStart), eol, start });
      start = i;
    } else {
      i += 1;
    }
  }
  if (start < n) {
    lines.push({ text: text.slice(start), eol: 'NONE', start });
  }
  return lines;
}

export function eolText(eol: Eol): string {
  switch (eol) {
    case 'LF': return '\n';
    case 'CRLF': return '\r\n';
    case 'CR': return '\r';
    default: return '';
  }
}

export function joinLines(lines: readonly Line[]): string {
  let out = '';
  for (const line of lines) out += line.text + eolText(line.eol);
  return out;
}
