/**
 * Line lexer for the indentation-based language.
 *
 * Line kinds: blank / comment (`# ...`) / code.
 * Indentation is spaces only; a tab in the indentation of a code line is an
 * error (recovered by treating each tab as TAB_WIDTH columns so the rest of
 * the file still parses).
 *
 * Brackets `()[]{}` carry a depth state across lines: while depth > 0 a line
 * is a continuation line and its indentation is ignored. `#` starts a
 * trailing comment (the language has no string literals).
 */

export type LineKind = 'blank' | 'comment' | 'code';

export interface LexedLine {
  kind: LineKind;
  /** Recovered indentation columns (tabs count as TAB_WIDTH). */
  indent: number;
  /** Trimmed line text (trailing comment included for code lines). */
  text: string;
  /** Bracket depth carried into this line. */
  depthIn: number;
  /** Bracket depth after this line. */
  depthOut: number;
  /** Tab found in indentation of a code line. */
  tabError: boolean;
  /** A closing bracket had no matching open bracket. */
  bracketError: boolean;
}

export const TAB_WIDTH = 4;

const OPENERS = new Set(['(', '[', '{']);
const CLOSERS = new Set([')', ']', '}']);

export function lexLine(source: string, depthIn: number): LexedLine {
  let indent = 0;
  let sawTab = false;
  let i = 0;
  while (i < source.length && (source[i] === ' ' || source[i] === '\t')) {
    if (source[i] === '\t') {
      sawTab = true;
      indent += TAB_WIDTH;
    } else {
      indent += 1;
    }
    i += 1;
  }
  const body = source.slice(i);
  const text = body.trim();

  if (text === '') {
    return { kind: 'blank', indent, text: '', depthIn, depthOut: depthIn, tabError: false, bracketError: false };
  }
  if (body[0] === '#') {
    return { kind: 'comment', indent, text, depthIn, depthOut: depthIn, tabError: false, bracketError: false };
  }

  let depth = depthIn;
  let bracketError = false;
  for (const ch of body) {
    if (ch === '#') break;
    if (OPENERS.has(ch)) {
      depth += 1;
    } else if (CLOSERS.has(ch)) {
      if (depth === 0) {
        bracketError = true;
      } else {
        depth -= 1;
      }
    }
  }
  return { kind: 'code', indent, text, depthIn, depthOut: depth, tabError: sawTab, bracketError };
}

export function lexedEquals(a: LexedLine, b: LexedLine): boolean {
  return (
    a.kind === b.kind &&
    a.indent === b.indent &&
    a.text === b.text &&
    a.depthIn === b.depthIn &&
    a.depthOut === b.depthOut &&
    a.tabError === b.tabError &&
    a.bracketError === b.bracketError
  );
}
