export type TokenType =
  | 'code'
  | 'open'
  | 'close'
  | 'string'
  | 'line-comment'
  | 'block-comment';

export interface Token {
  type: TokenType;
  /** Inclusive UTF-16 start offset. */
  start: number;
  /** Exclusive UTF-16 end offset. */
  end: number;
  /** Bracket character, present on 'open' and 'close' tokens. */
  char?: string;
  /** True for strings/comments that hit end-of-line or EOF before closing. */
  unterminated?: boolean;
}

/** Lexer state carried across token boundaries. */
export type LexState = 0 | 1;
export const LS_CODE: LexState = 0;
export const LS_BLOCK_COMMENT: LexState = 1;

export const OPENERS = '([{';
export const CLOSERS = ')]}';
export const MATCH_CLOSE: Readonly<Record<string, string>> = { '(': ')', '[': ']', '{': '}' };
export const MATCH_OPEN: Readonly<Record<string, string>> = { ')': '(', ']': '[', '}': '{' };

function isBracket(ch: string): boolean {
  return ch === '(' || ch === ')' || ch === '[' || ch === ']' || ch === '{' || ch === '}';
}

/** Characters that terminate a run of plain code. */
function isSpecial(ch: string): boolean {
  return isBracket(ch) || ch === '"' || ch === "'" || ch === '/';
}

/**
 * Lexes exactly one token starting at `pos` (which must be a token boundary
 * described by `state`). Pure function over the current text.
 */
export function lexToken(text: string, pos: number, state: LexState): { token: Token; state: LexState } {
  const n = text.length;
  if (state === LS_BLOCK_COMMENT) {
    const close = text.indexOf('*/', pos);
    if (close === -1) {
      return { token: { type: 'block-comment', start: pos, end: n, unterminated: true }, state: LS_BLOCK_COMMENT };
    }
    return { token: { type: 'block-comment', start: pos, end: close + 2 }, state: LS_CODE };
  }

  const ch = text[pos];

  if (OPENERS.includes(ch)) {
    return { token: { type: 'open', start: pos, end: pos + 1, char: ch }, state: LS_CODE };
  }
  if (CLOSERS.includes(ch)) {
    return { token: { type: 'close', start: pos, end: pos + 1, char: ch }, state: LS_CODE };
  }

  if (ch === '"' || ch === "'") {
    let i = pos + 1;
    while (i < n) {
      const c = text[i];
      if (c === '\\') {
        i += 2; // escape sequence: the next char cannot end the string
        continue;
      }
      if (c === ch) {
        return { token: { type: 'string', start: pos, end: i + 1 }, state: LS_CODE };
      }
      if (c === '\n' || c === '\r') break; // strings do not span lines
      i++;
    }
    return { token: { type: 'string', start: pos, end: i, unterminated: true }, state: LS_CODE };
  }

  if (ch === '/' && text[pos + 1] === '/') {
    let i = pos + 2;
    while (i < n && text[i] !== '\n' && text[i] !== '\r') i++;
    return { token: { type: 'line-comment', start: pos, end: i }, state: LS_CODE };
  }

  if (ch === '/' && text[pos + 1] === '*') {
    const close = text.indexOf('*/', pos + 2);
    if (close === -1) {
      return { token: { type: 'block-comment', start: pos, end: n, unterminated: true }, state: LS_BLOCK_COMMENT };
    }
    return { token: { type: 'block-comment', start: pos, end: close + 2 }, state: LS_CODE };
  }

  let i = pos + 1;
  while (i < n && !isSpecial(text[i])) i++;
  return { token: { type: 'code', start: pos, end: i }, state: LS_CODE };
}

/** Lexes the whole text from scratch. Used as the reference for tests. */
export function lexAll(text: string): Token[] {
  const tokens: Token[] = [];
  let pos = 0;
  let state: LexState = LS_CODE;
  while (pos < text.length) {
    const result = lexToken(text, pos, state);
    tokens.push(result.token);
    pos = result.token.end;
    state = result.state;
  }
  return tokens;
}

/**
 * Incremental lexer. Keeps a token list plus the lex state at each token
 * start, so a local edit only re-lexes from the touched token until the
 * token stream synchronizes with the previous one again.
 */
export class IncrementalLexer {
  private tokens: Token[] = [];
  private states: LexState[] = [];

  constructor(text: string) {
    this.setText(text);
  }

  /** Replaces the entire text and re-lexes from scratch. */
  setText(text: string): void {
    this.tokens = [];
    this.states = [];
    let pos = 0;
    let state: LexState = LS_CODE;
    while (pos < text.length) {
      const result = lexToken(text, pos, state);
      this.tokens.push(result.token);
      this.states.push(state);
      pos = result.token.end;
      state = result.state;
    }
  }

  getTokens(): readonly Token[] {
    return this.tokens;
  }

  /**
   * Notifies the lexer that `text` (the NEW text) resulted from replacing
   * the old range [start, oldEnd) with `newLength` UTF-16 code units.
   */
  applyEdit(text: string, start: number, oldEnd: number, newLength: number): void {
    const delta = newLength - (oldEnd - start);
    const newEnd = start + newLength;

    // Re-lex from the last token that starts before the edit. Backing up one
    // token also covers edits at a token boundary that merge with the
    // previous token (e.g. inserting "*" right after a "/").
    let replaceFrom = 0;
    let low = 0;
    let high = this.tokens.length - 1;
    let found = -1;
    while (low <= high) {
      const mid = (low + high) >> 1;
      if (this.tokens[mid].start < start) {
        found = mid;
        low = mid + 1;
      } else {
        high = mid - 1;
      }
    }
    replaceFrom = found === -1 ? 0 : found;

    const relexStart = found === -1 ? 0 : this.tokens[found].start;
    let state: LexState = found === -1 ? LS_CODE : this.states[found];

    // First old token that may serve as a resynchronization point: it must
    // start at or after the old edit end (i.e. in untouched text).
    let scan = replaceFrom;
    while (scan < this.tokens.length && this.tokens[scan].start < oldEnd) scan++;

    const freshTokens: Token[] = [];
    const freshStates: LexState[] = [];
    let pos = relexStart;
    while (pos < text.length) {
      const result = lexToken(text, pos, state);
      freshTokens.push(result.token);
      freshStates.push(state);
      pos = result.token.end;
      state = result.state;
      if (pos >= newEnd) {
        // Try to resynchronize with the old token stream (shifted by delta).
        while (scan < this.tokens.length && this.tokens[scan].start + delta < pos) scan++;
        if (
          scan < this.tokens.length &&
          this.tokens[scan].start + delta === pos &&
          this.states[scan] === state
        ) {
          break;
        }
      }
    }

    const replacedCount = scan - replaceFrom;
    this.tokens.splice(replaceFrom, replacedCount, ...freshTokens);
    this.states.splice(replaceFrom, replacedCount, ...freshStates);
    for (let i = replaceFrom + freshTokens.length; i < this.tokens.length; i++) {
      this.tokens[i].start += delta;
      this.tokens[i].end += delta;
    }
  }
}
