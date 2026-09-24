/**
 * Bracket pairing and mismatch diagnostics for a small language with
 * strings and comments.
 *
 * Language lexical rules:
 *   - Line comments:   `//` ... end of line (LF, CRLF or lone CR)
 *   - Block comments:  `/*` ... `* /` (non-nesting)
 *   - Strings:         "double" or 'single' quoted, `\` escapes the next char
 *   - Brackets:        ( ) [ ] { }
 *
 * Brackets inside strings or comments never participate in matching.
 *
 * All public offsets are UTF-16 code-unit offsets (i.e. JavaScript string
 * indices). Any API that takes an offset rejects offsets that would split a
 * surrogate pair.
 *
 * All matching and traversal is iterative (explicit stacks, no recursion),
 * so nesting depths of 10_000+ are safe.
 */

export interface BracketPair {
  /** UTF-16 offset of the opening bracket. */
  open: number;
  /** UTF-16 offset of the closing bracket. */
  close: number;
  openChar: string;
  closeChar: string;
}

export type DiagnosticCode =
  | 'unmatched-closing'
  | 'unclosed-opening'
  | 'unterminated-string'
  | 'unterminated-comment';

export interface Diagnostic {
  code: DiagnosticCode;
  message: string;
  /** UTF-16 offset, inclusive. */
  start: number;
  /** UTF-16 offset, exclusive. */
  end: number;
}

export interface Position {
  /** 0-based line number. */
  line: number;
  /** 0-based column, counted in UTF-16 code units. */
  character: number;
}

interface BracketToken {
  offset: number;
  char: string;
}

interface PairNode {
  pair: BracketPair;
  children: PairNode[];
}

const OPENERS = new Set(['(', '[', '{']);
const CLOSERS = new Set([')', ']', '}']);
const CLOSER_TO_OPENER = new Map([
  [')', '('],
  [']', '['],
  ['}', '{'],
]);

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` falls between a high and a low surrogate. */
function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/** Tokenize brackets, skipping strings and comments. */
function lex(text: string): { tokens: BracketToken[]; diagnostics: Diagnostic[] } {
  const tokens: BracketToken[] = [];
  const diagnostics: Diagnostic[] = [];
  const n = text.length;
  let i = 0;
  while (i < n) {
    const ch = text[i];
    if (ch === '/' && i + 1 < n && text[i + 1] === '/') {
      i += 2;
      while (i < n && text[i] !== '\n' && text[i] !== '\r') i++;
    } else if (ch === '/' && i + 1 < n && text[i + 1] === '*') {
      const start = i;
      i += 2;
      let closed = false;
      while (i < n) {
        if (text[i] === '*' && i + 1 < n && text[i + 1] === '/') {
          i += 2;
          closed = true;
          break;
        }
        i++;
      }
      if (!closed) {
        diagnostics.push({
          code: 'unterminated-comment',
          message: 'Unterminated block comment',
          start,
          end: n,
        });
      }
    } else if (ch === '"' || ch === "'") {
      const quote = ch;
      const start = i;
      i++;
      let closed = false;
      while (i < n) {
        const c = text[i];
        if (c === '\\') {
          i += 2;
          continue;
        }
        if (c === quote) {
          i++;
          closed = true;
          break;
        }
        if (c === '\n' || c === '\r') break;
        i++;
      }
      if (!closed) {
        diagnostics.push({
          code: 'unterminated-string',
          message: 'Unterminated string literal',
          start,
          end: Math.min(i, n),
        });
      }
    } else if (OPENERS.has(ch) || CLOSERS.has(ch)) {
      tokens.push({ offset: i, char: ch });
      i++;
    } else {
      i++;
    }
  }
  return { tokens, diagnostics };
}

/**
 * Match bracket tokens with an explicit stack.
 *
 * Recovery strategy for a closing bracket that does not match the innermost
 * opener: if a matching opener exists further out, the intervening openers
 * are reported as `unclosed-opening` and the pair is matched; otherwise the
 * closing bracket is reported as `unmatched-closing`.
 */
function matchPairs(tokens: BracketToken[]): { pairs: BracketPair[]; diagnostics: Diagnostic[] } {
  const pairs: BracketPair[] = [];
  const diagnostics: Diagnostic[] = [];
  const stack: BracketToken[] = [];
  for (const token of tokens) {
    if (OPENERS.has(token.char)) {
      stack.push(token);
      continue;
    }
    const want = CLOSER_TO_OPENER.get(token.char)!;
    const top = stack[stack.length - 1];
    if (top !== undefined && top.char === want) {
      stack.pop();
      pairs.push({ open: top.offset, close: token.offset, openChar: top.char, closeChar: token.char });
      continue;
    }
    let found = -1;
    for (let k = stack.length - 1; k >= 0; k--) {
      if (stack[k].char === want) {
        found = k;
        break;
      }
    }
    if (found === -1) {
      diagnostics.push({
        code: 'unmatched-closing',
        message: `Unmatched closing bracket '${token.char}'`,
        start: token.offset,
        end: token.offset + 1,
      });
    } else {
      const opener = stack[found];
      for (let k = stack.length - 1; k > found; k--) {
        const dropped = stack[k];
        diagnostics.push({
          code: 'unclosed-opening',
          message: `Opening bracket '${dropped.char}' is never closed`,
          start: dropped.offset,
          end: dropped.offset + 1,
        });
      }
      stack.length = found;
      pairs.push({ open: opener.offset, close: token.offset, openChar: opener.char, closeChar: token.char });
    }
  }
  for (const leftover of stack) {
    diagnostics.push({
      code: 'unclosed-opening',
      message: `Opening bracket '${leftover.char}' is never closed`,
      start: leftover.offset,
      end: leftover.offset + 1,
    });
  }
  return { pairs, diagnostics };
}

/** Build a forest of properly nested pairs. Input must be sorted by `open`. */
function buildForest(sortedPairs: BracketPair[]): PairNode[] {
  const roots: PairNode[] = [];
  const stack: PairNode[] = [];
  for (const pair of sortedPairs) {
    const node: PairNode = { pair, children: [] };
    while (stack.length > 0 && stack[stack.length - 1].pair.close < pair.open) {
      stack.pop();
    }
    if (stack.length > 0) {
      stack[stack.length - 1].children.push(node);
    } else {
      roots.push(node);
    }
    stack.push(node);
  }
  return roots;
}

/** Line start offsets; handles LF, CRLF and lone CR. */
function computeLineStarts(text: string): number[] {
  const starts = [0];
  let i = 0;
  while (i < text.length) {
    const c = text[i];
    if (c === '\r') {
      if (i + 1 < text.length && text[i + 1] === '\n') i++;
      starts.push(i + 1);
    } else if (c === '\n') {
      starts.push(i + 1);
    }
    i++;
  }
  return starts;
}

export class BracketAnalyzer {
  #text: string;
  #pairs: BracketPair[] = [];
  #diagnostics: Diagnostic[] = [];
  #roots: PairNode[] = [];
  #lineStarts: number[] | null = null;

  constructor(text: string) {
    if (typeof text !== 'string') {
      throw new TypeError('BracketAnalyzer expects a string');
    }
    this.#text = text;
    this.#reanalyze();
  }

  getText(): string {
    return this.#text;
  }

  /** Matched pairs, sorted by opening offset. */
  getPairs(): readonly BracketPair[] {
    return this.#pairs;
  }

  /** Diagnostics, sorted by start offset. */
  getDiagnostics(): readonly Diagnostic[] {
    return this.#diagnostics;
  }

  /**
   * Replace the UTF-16 range [start, end) with `replacement` and re-analyze.
   * Both offsets must be valid text boundaries (no surrogate splitting).
   */
  applyEdit(start: number, end: number, replacement: string): void {
    this.#assertBoundary(start, 'start');
    this.#assertBoundary(end, 'end');
    if (start > end) {
      throw new RangeError(`start (${start}) must not exceed end (${end})`);
    }
    this.#text = this.#text.slice(0, start) + replacement + this.#text.slice(end);
    this.#reanalyze();
  }

  /**
   * Return the smallest matched pair whose [open, close] span contains
   * `offset`, or null when the offset is not inside any pair.
   */
  enclosingPair(offset: number): BracketPair | null {
    this.#assertBoundary(offset, 'offset');
    let candidates = this.#roots;
    let best: BracketPair | null = null;
    for (;;) {
      let lo = 0;
      let hi = candidates.length - 1;
      let idx = -1;
      while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (candidates[mid].pair.open <= offset) {
          idx = mid;
          lo = mid + 1;
        } else {
          hi = mid - 1;
        }
      }
      if (idx === -1) break;
      const node = candidates[idx];
      if (node.pair.close < offset) break;
      best = node.pair;
      candidates = node.children;
    }
    return best;
  }

  /** Convert a UTF-16 offset to a 0-based line/character position. */
  positionAt(offset: number): Position {
    this.#assertBoundary(offset, 'offset');
    const starts = this.#getLineStarts();
    let lo = 0;
    let hi = starts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (starts[mid] <= offset) lo = mid;
      else hi = mid - 1;
    }
    return { line: lo, character: offset - starts[lo] };
  }

  /**
   * Convert a 0-based line/character position to a UTF-16 offset.
   * Line and character are clamped to valid ranges; the character is clamped
   * to the line's content length (excluding the line break). Throws if the
   * resulting offset would split a surrogate pair.
   */
  offsetAt(position: Position): number {
    const starts = this.#getLineStarts();
    const line = Math.min(Math.max(0, Math.trunc(position.line)), starts.length - 1);
    const lineStart = starts[line];
    let contentEnd = lineStart;
    while (
      contentEnd < this.#text.length &&
      this.#text[contentEnd] !== '\n' &&
      this.#text[contentEnd] !== '\r'
    ) {
      contentEnd++;
    }
    const character = Math.min(Math.max(0, Math.trunc(position.character)), contentEnd - lineStart);
    const offset = lineStart + character;
    if (splitsSurrogatePair(this.#text, offset)) {
      throw new RangeError(`Position resolves to offset ${offset}, which splits a surrogate pair`);
    }
    return offset;
  }

  #getLineStarts(): number[] {
    if (this.#lineStarts === null) {
      this.#lineStarts = computeLineStarts(this.#text);
    }
    return this.#lineStarts;
  }

  #assertBoundary(offset: number, name: string): void {
    if (!Number.isInteger(offset) || offset < 0 || offset > this.#text.length) {
      throw new RangeError(
        `${name} (${offset}) is out of range [0, ${this.#text.length}]`,
      );
    }
    if (splitsSurrogatePair(this.#text, offset)) {
      throw new RangeError(`${name} (${offset}) splits a surrogate pair`);
    }
  }

  #reanalyze(): void {
    const { tokens, diagnostics: lexDiagnostics } = lex(this.#text);
    const { pairs, diagnostics: matchDiagnostics } = matchPairs(tokens);
    pairs.sort((a, b) => a.open - b.open || a.close - b.close);
    this.#pairs = pairs;
    this.#diagnostics = [...lexDiagnostics, ...matchDiagnostics].sort(
      (a, b) => a.start - b.start || a.end - b.end,
    );
    this.#roots = buildForest(pairs);
    this.#lineStarts = null;
  }
}
