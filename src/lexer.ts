/**
 * Character-level lexical scanner.
 *
 * Directives are line comments of the form `// #if|#elif|#else|#endif ...`
 * that appear as the first non-whitespace tokens of a line *while the
 * scanner is in top-level code state*. Because the scanner carries a real
 * lexical state across lines (block comments, strings, template literals
 * including `${}` interpolation), pseudo-directives inside strings or
 * multi-line comments are never mistaken for real ones. This is a state
 * machine over the raw character stream, not a per-line regex.
 */

export type LexState = 'code' | 'lineComment' | 'blockComment' | 'string' | 'template';

export type DirectiveKind = 'if' | 'elif' | 'else' | 'endif';

export interface Directive {
  kind: DirectiveKind;
  /** Raw text after the directive keyword, trimmed. */
  argument: string;
  /** 1-based line number. */
  line: number;
  /** 0-based column of the `#` character. */
  column: number;
}

export interface ScannedLine {
  /** 1-based line number. */
  line: number;
  /** Raw line text without the line terminator (a trailing `\r` is stripped). */
  text: string;
  /** Lexical state at the start of this line. */
  startState: LexState;
  /** The directive on this line, or null when the line is ordinary source. */
  directive: Directive | null;
}

function isLowerAlpha(ch: string): boolean {
  return ch >= 'a' && ch <= 'z';
}

function matchDirective(text: string, line: number): Directive | null {
  let j = 0;
  while (j < text.length && (text[j] === ' ' || text[j] === '\t')) j++;
  if (text[j] !== '/' || text[j + 1] !== '/') return null;
  j += 2;
  while (j < text.length && (text[j] === ' ' || text[j] === '\t')) j++;
  if (text[j] !== '#') return null;
  const column = j;
  j++;
  const kwStart = j;
  while (j < text.length && isLowerAlpha(text[j])) j++;
  const kw = text.slice(kwStart, j);
  if (kw !== 'if' && kw !== 'elif' && kw !== 'else' && kw !== 'endif') return null;
  if (j < text.length && text[j] !== ' ' && text[j] !== '\t') return null;
  return { kind: kw, argument: text.slice(j).trim(), line, column };
}

export function scanLines(source: string): ScannedLine[] {
  const lines: ScannedLine[] = [];
  let state: LexState = 'code';
  // Brace depths at which `${ ... }` interpolations were opened; when a `}`
  // in code state brings the depth back to the top entry, the interpolation
  // ends and scanning resumes in template state.
  const templateDepths: number[] = [];
  let braceDepth = 0;
  let stringQuote = '';
  let lineNumber = 1;
  let lineStart = 0;
  const n = source.length;

  while (lineStart < n) {
    let lineEnd = source.indexOf('\n', lineStart);
    if (lineEnd === -1) lineEnd = n;
    let text = source.slice(lineStart, lineEnd);
    if (text.endsWith('\r')) text = text.slice(0, -1);

    const startState = state;
    let directive: Directive | null = null;
    if (state === 'code' && templateDepths.length === 0) {
      directive = matchDirective(text, lineNumber);
    }

    if (directive === null) {
      for (let i = lineStart; i < lineEnd; i++) {
        const ch = source[i];
        const next = i + 1 < n ? source[i + 1] : '';
        if (state === 'code') {
          if (ch === '/' && next === '/') {
            state = 'lineComment';
            i++;
          } else if (ch === '/' && next === '*') {
            state = 'blockComment';
            i++;
          } else if (ch === "'" || ch === '"') {
            state = 'string';
            stringQuote = ch;
          } else if (ch === '`') {
            state = 'template';
          } else if (ch === '{') {
            if (templateDepths.length > 0) braceDepth++;
          } else if (ch === '}') {
            if (templateDepths.length > 0) {
              if (braceDepth === templateDepths[templateDepths.length - 1]) {
                templateDepths.pop();
                state = 'template';
              } else {
                braceDepth--;
              }
            }
          }
        } else if (state === 'lineComment') {
          // Runs to end of line; nothing to do.
        } else if (state === 'blockComment') {
          if (ch === '*' && next === '/') {
            state = 'code';
            i++;
          }
        } else if (state === 'string') {
          if (ch === '\\') {
            i++;
          } else if (ch === stringQuote) {
            state = 'code';
          }
        } else {
          // template
          if (ch === '\\') {
            i++;
          } else if (ch === '`') {
            state = 'code';
          } else if (ch === '$' && next === '{') {
            templateDepths.push(braceDepth);
            state = 'code';
            i++;
          }
        }
      }
    }

    lines.push({ line: lineNumber, text, startState, directive });

    // Newline always terminates line comments; unterminated string literals
    // are treated leniently and also end at the newline.
    if (state === 'lineComment' || state === 'string') state = 'code';
    lineStart = lineEnd + 1;
    lineNumber++;
  }

  return lines;
}
