/**
 * Incremental line-state lexer for a tiny language.
 *
 * Language features:
 *   - identifiers / keywords (let, fn, if, else, return, while, true, false)
 *   - numbers
 *   - double-quoted strings with backslash escapes (\" does NOT end a string);
 *     strings cannot span lines (an unterminated string ends at end-of-line)
 *   - line comments  // ...
 *   - block comments /* ... *​/ which may span multiple lines (non-nesting)
 *
 * Public offsets are UTF-16 code-unit offsets (i.e. plain JS string indices).
 * Edits that would split a surrogate pair are rejected.
 * Both LF and CRLF (and lone CR) line endings are supported.
 */

export type TokenType =
  | 'keyword'
  | 'identifier'
  | 'number'
  | 'string'
  | 'comment'
  | 'punctuation';

export interface Token {
  type: TokenType;
  /** inclusive start, UTF-16 code units from document start */
  start: number;
  /** exclusive end, UTF-16 code units from document start */
  end: number;
}

/** Lexer state carried across a line boundary. */
export type LineState = 'code' | 'comment';

interface LineCache {
  /** state at the start of the line */
  entry: LineState;
  /** state at the end of the line */
  exit: LineState;
  /**
   * Tokens with offsets RELATIVE to the line start, so that edits shifting
   * the line's absolute position do not stale the cache. Public APIs add the
   * line's absolute start back to produce UTF-16 document offsets.
   */
  tokens: Token[];
  valid: boolean;
}

export interface EditResult {
  /** 0-based line numbers whose tokens were recomputed and may need re-coloring */
  invalidatedLines: number[];
}

const KEYWORDS = new Set([
  'let', 'fn', 'if', 'else', 'return', 'while', 'true', 'false',
]);

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

function isDigit(cp: number): boolean {
  return cp >= 0x30 && cp <= 0x39;
}

function isIdentStart(cp: number): boolean {
  return (
    (cp >= 0x61 && cp <= 0x7a) || // a-z
    (cp >= 0x41 && cp <= 0x5a) || // A-Z
    cp === 0x5f || // _
    cp >= 0x80 // any non-ASCII code point (incl. astral) can start an identifier
  );
}

function isIdentPart(cp: number): boolean {
  return isIdentStart(cp) || isDigit(cp);
}

/**
 * Lex a single line. `lineText` excludes the line terminator; `lineStart` is
 * the UTF-16 offset of the line's first character in the document.
 */
function lexLine(
  lineText: string,
  lineStart: number,
  entry: LineState,
): { tokens: Token[]; exit: LineState } {
  const tokens: Token[] = [];
  const n = lineText.length;
  let i = 0;

  const push = (type: TokenType, from: number, to: number) => {
    if (to > from) tokens.push({ type, start: lineStart + from, end: lineStart + to });
  };

  // A line entered inside a block comment: scan for the terminator first.
  if (entry === 'comment') {
    const close = lineText.indexOf('*/', 0);
    if (close === -1) {
      push('comment', 0, n);
      return { tokens, exit: 'comment' };
    }
    push('comment', 0, close + 2);
    i = close + 2;
  }

  while (i < n) {
    const ch = lineText.charCodeAt(i);

    // whitespace
    if (ch === 0x20 || ch === 0x09 || ch === 0x0b || ch === 0x0c) {
      i++;
      continue;
    }

    // comments
    if (ch === 0x2f /* / */ && i + 1 < n) {
      const next = lineText.charCodeAt(i + 1);
      if (next === 0x2f) {
        // line comment: runs to end of line
        push('comment', i, n);
        return { tokens, exit: 'code' };
      }
      if (next === 0x2a /* * */) {
        // block comment: the terminator search starts past the opener so
        // that "/*/" is (correctly) an unterminated comment
        const close = lineText.indexOf('*/', i + 2);
        if (close === -1) {
          push('comment', i, n);
          return { tokens, exit: 'comment' };
        }
        push('comment', i, close + 2);
        i = close + 2;
        continue;
      }
    }

    // string literal
    if (ch === 0x22 /* " */) {
      let j = i + 1;
      while (j < n) {
        const c = lineText.charCodeAt(j);
        if (c === 0x5c /* \ */) {
          j += 2; // escaped char (incl. \") never terminates the string
          continue;
        }
        if (c === 0x22) {
          j++;
          break;
        }
        j++;
      }
      // an unterminated string ends at EOL; the line state stays 'code'
      push('string', i, Math.min(j, n));
      i = Math.min(j, n);
      continue;
    }

    // number
    if (isDigit(ch)) {
      let j = i + 1;
      while (j < n) {
        const c = lineText.charCodeAt(j);
        if (isDigit(c) || c === 0x2e /* . */) j++;
        else break;
      }
      push('number', i, j);
      i = j;
      continue;
    }

    // identifier / keyword (astral-aware so surrogate pairs are never split)
    const cp = lineText.codePointAt(i)!;
    if (isIdentStart(cp)) {
      let j = i + (cp > 0xffff ? 2 : 1);
      while (j < n) {
        const c = lineText.codePointAt(j)!;
        if (!isIdentPart(c)) break;
        j += c > 0xffff ? 2 : 1;
      }
      const word = lineText.slice(i, j);
      push(KEYWORDS.has(word) ? 'keyword' : 'identifier', i, j);
      i = j;
      continue;
    }

    // punctuation: single code point
    push('punctuation', i, i + (cp > 0xffff ? 2 : 1));
    i += cp > 0xffff ? 2 : 1;
  }

  return { tokens, exit: 'code' };
}

export class IncrementalLexer {
  private text = '';
  /** UTF-16 offset of the first char of each line; lineStarts[0] === 0 */
  private lineStarts: number[] = [0];
  private cache: LineCache[] = [];

  constructor(text = '') {
    this.setText(text);
  }

  /** Replace the whole document (non-incremental). */
  setText(text: string): void {
    this.text = text;
    this.recomputeLineStarts();
    this.cache = this.lineStarts.map(() => ({
      entry: 'code',
      exit: 'code',
      tokens: [],
      valid: false,
    }));
    this.propagate(0);
  }

  getText(): string {
    return this.text;
  }

  getLineCount(): number {
    return this.lineStarts.length;
  }

  getLineStart(line: number): number {
    return this.lineStarts[line];
  }

  /** All tokens in the document, in order, with absolute UTF-16 offsets. */
  getTokens(): Token[] {
    this.propagate(0);
    const out: Token[] = [];
    for (let i = 0; i < this.cache.length; i++) out.push(...this.absoluteTokens(i));
    return out;
  }

  /** Tokens of one line, with absolute UTF-16 offsets. */
  getLineTokens(line: number): Token[] {
    this.propagate(0);
    return this.absoluteTokens(line);
  }

  private absoluteTokens(line: number): Token[] {
    const base = this.lineStarts[line];
    return this.cache[line].tokens.map((t) => ({
      type: t.type,
      start: base + t.start,
      end: base + t.end,
    }));
  }

  getLineState(line: number): { entry: LineState; exit: LineState } {
    this.propagate(0);
    const c = this.cache[line];
    return { entry: c.entry, exit: c.exit };
  }

  /**
   * Apply an edit: replace text[start..end) with `inserted`.
   * All offsets are UTF-16 code units. Boundaries that fall inside a
   * surrogate pair are rejected. Re-lexing starts at the first touched line
   * and propagates forward only until the line states stabilize.
   */
  applyEdit(start: number, end: number, inserted: string): EditResult {
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start || end > this.text.length) {
      throw new RangeError(`invalid edit range [${start}, ${end}) for document of length ${this.text.length}`);
    }
    this.assertNotInsideSurrogatePair(start);
    this.assertNotInsideSurrogatePair(end);

    const oldStartLine = this.lineAt(start);
    const oldEndLine = this.lineAt(end);
    const oldLineCount = this.lineStarts.length;

    this.text = this.text.slice(0, start) + inserted + this.text.slice(end);
    this.recomputeLineStarts();

    const delta = this.lineStarts.length - oldLineCount;
    const removedCount = oldEndLine - oldStartLine + 1;
    const insertedCount = removedCount + delta;
    const blanks: LineCache[] = Array.from({ length: insertedCount }, () => ({
      entry: 'code',
      exit: 'code',
      tokens: [],
      valid: false,
    }));
    this.cache.splice(oldStartLine, removedCount, ...blanks);

    const invalidatedLines = this.propagate(oldStartLine);
    return { invalidatedLines };
  }

  private assertNotInsideSurrogatePair(boundary: number): void {
    if (
      boundary > 0 &&
      boundary < this.text.length &&
      isHighSurrogate(this.text.charCodeAt(boundary - 1)) &&
      isLowSurrogate(this.text.charCodeAt(boundary))
    ) {
      throw new RangeError(
        `edit boundary ${boundary} splits a surrogate pair; offsets must not divide a UTF-16 surrogate pair`,
      );
    }
  }

  private recomputeLineStarts(): void {
    const starts = [0];
    const t = this.text;
    for (let i = 0; i < t.length; i++) {
      const c = t.charCodeAt(i);
      if (c === 0x0d /* \r */) {
        if (i + 1 < t.length && t.charCodeAt(i + 1) === 0x0a) i++; // CRLF
        starts.push(i + 1);
      } else if (c === 0x0a /* \n */) {
        starts.push(i + 1);
      }
    }
    this.lineStarts = starts;
  }

  /** 0-based index of the line containing UTF-16 offset `offset`. */
  private lineAt(offset: number): number {
    let lo = 0;
    let hi = this.lineStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.lineStarts[mid] <= offset) lo = mid;
      else hi = mid - 1;
    }
    return lo;
  }

  /** Text of a line without its terminator. */
  private lineText(line: number): string {
    const start = this.lineStarts[line];
    let end =
      line + 1 < this.lineStarts.length ? this.lineStarts[line + 1] : this.text.length;
    if (end > start && this.text.charCodeAt(end - 1) === 0x0a) end--;
    if (end > start && this.text.charCodeAt(end - 1) === 0x0d) end--;
    return this.text.slice(start, end);
  }

  /**
   * Re-lex from `fromLine` forward, stopping as soon as a cached line is
   * reached whose entry state already matches — everything beyond is stable.
   * Returns the list of lines that were actually re-lexed.
   */
  private propagate(fromLine: number): number[] {
    const invalidated: number[] = [];
    let entry: LineState = fromLine === 0 ? 'code' : this.cache[fromLine - 1].exit;
    let i = fromLine;
    while (i < this.lineStarts.length) {
      const c = this.cache[i];
      if (c.valid && c.entry === entry) break; // stable: downstream is unchanged
      // offsets are cached relative to the line start (base 0)
      const { tokens, exit } = lexLine(this.lineText(i), 0, entry);
      c.entry = entry;
      c.exit = exit;
      c.tokens = tokens;
      c.valid = true;
      invalidated.push(i);
      entry = exit;
      i++;
    }
    return invalidated;
  }
}
