/**
 * Core character-level scanner.
 *
 * All offsets are absolute UTF-16 code-unit offsets (i.e. JavaScript string
 * indices; an astral character such as an emoji counts as 2).
 *
 * The machine is a pure function of (state, next character): it never needs
 * lookahead beyond the current character, so input may be split into chunks
 * at ANY boundary — including between the two characters of the comment
 * open/close markers — and the result is identical to a single-shot scan.
 */

export type TokenKind = 'code' | 'string' | 'lineComment' | 'blockComment';

export interface Token {
  kind: TokenKind;
  /** Absolute UTF-16 offset of the first character (inclusive). */
  start: number;
  /** Absolute UTF-16 offset one past the last character (exclusive). */
  end: number;
}

/**
 * Fine-grained modes. The `*Slash` / `*Star` variants remember a just-seen
 * `/` or `*` so two-character markers survive chunk splits.
 */
export type Mode =
  | 'code'
  | 'codeSlash'        // code, previous char was '/'
  | 'string'
  | 'lineComment'
  | 'blockComment'
  | 'blockCommentSlash' // inside block comment, previous char was '/'
  | 'blockCommentStar'; // inside block comment, previous char was '*'

/**
 * Complete, serializable scanner state. Capturing this at any point (a
 * "checkpoint") and later resuming from it yields exactly the same tokens as
 * one continuous scan. It records everything needed mid-construct:
 *  - `tokenStart`: start of the pending token (unfinished string, comment or
 *    code run),
 *  - `commentStack`: absolute starts of every unclosed `/*` (nested comments),
 *  - `offset`: absolute source offset of the next character to read.
 */
export interface ScannerState {
  offset: number;
  mode: Mode;
  tokenStart: number;
  /** Inside a string: the previous character was an unescaped backslash. */
  escaped: boolean;
  /** Absolute start offsets of currently unclosed `/*` markers, outermost first. */
  commentStack: number[];
}

/** A construct left unclosed at end of input. */
export interface Unterminated {
  kind: 'string' | 'blockComment';
  /** Absolute UTF-16 offset where the unclosed construct starts. */
  start: number;
}

export function initialState(offset = 0): ScannerState {
  return { offset, mode: 'code', tokenStart: offset, escaped: false, commentStack: [] };
}

export function cloneState(state: ScannerState): ScannerState {
  return { ...state, commentStack: state.commentStack.slice() };
}

export function statesEqual(a: ScannerState, b: ScannerState): boolean {
  if (a.offset !== b.offset || a.mode !== b.mode || a.tokenStart !== b.tokenStart ||
      a.escaped !== b.escaped || a.commentStack.length !== b.commentStack.length) {
    return false;
  }
  for (let i = 0; i < a.commentStack.length; i++) {
    if (a.commentStack[i] !== b.commentStack[i]) return false;
  }
  return true;
}

/**
 * Token sink. Besides the token, it receives the true machine state at
 * `token.end` — a valid resumption checkpoint: re-scanning any text from
 * `token.end` with that state behaves exactly as if the scan had continued
 * from the beginning. Note the state's `tokenStart` may be smaller than
 * `token.end` for a `code` token, because a code run is only finalized once
 * the following construct's opener is seen; resuming re-emits the run.
 */
export type TokenSink = (token: Token, boundary: ScannerState) => void;

function boundary(offset: number, mode: Mode, tokenStart: number): ScannerState {
  return { offset, mode, tokenStart, escaped: false, commentStack: [] };
}

const QUOTE = 0x22; // "
const BACKSLASH = 0x5c; // \
const SLASH = 0x2f; // /
const STAR = 0x2a; // *
const NEWLINE = 0x0a; // \n

export class Scanner {
  state: ScannerState;
  private stopRequested = false;

  constructor(state?: ScannerState) {
    this.state = state ? cloneState(state) : initialState();
  }

  /**
   * Ask the scanner to stop after the current character, e.g. from inside a
   * token sink once a rescan has reached a stable point. The next `scan` or
   * `scanRange` call clears the request.
   */
  requestStop(): void {
    this.stopRequested = true;
  }

  /**
   * Consume `text` in full; `text[0]` sits at absolute offset `baseOffset`,
   * which must equal the current state offset. Emits every token that
   * completes within the chunk; a trailing unfinished construct stays pending
   * in the state and is carried into the next `scan` call (or `finish`).
   */
  scan(text: string, baseOffset: number, emit: TokenSink): void {
    this.run(text, 0, text.length, baseOffset, emit);
  }

  /**
   * Consume `text[from..to)` where `from` is an absolute offset into `text`
   * and must equal the current state offset. Used to re-scan a slice of a
   * larger buffer without copying.
   */
  scanRange(text: string, from: number, to: number, emit: TokenSink): void {
    this.run(text, from, to, 0, emit);
  }

  private run(text: string, i0: number, i1: number, posBase: number, emit: TokenSink): void {
    const st = this.state;
    if (posBase + i0 !== st.offset) {
      throw new Error(`Scanner at offset ${st.offset}, got chunk based at ${posBase + i0}`);
    }
    this.stopRequested = false;
    let i = i0;
    while (i < i1 && !this.stopRequested) {
      const c = text.charCodeAt(i);
      const pos = posBase + i;
      let advance = true;
      switch (st.mode) {
        case 'code':
          if (c === QUOTE) {
            const codeStart = st.tokenStart;
            st.mode = 'string';
            st.tokenStart = pos;
            st.escaped = false;
            if (codeStart < pos) {
              emit({ kind: 'code', start: codeStart, end: pos }, boundary(pos, 'code', codeStart));
            }
          } else if (c === SLASH) {
            st.mode = 'codeSlash';
          }
          break;
        case 'codeSlash':
          if (c === SLASH) {
            const codeStart = st.tokenStart;
            st.mode = 'lineComment';
            st.tokenStart = pos - 1;
            if (codeStart < pos - 1) {
              // The boundary sits before the '/', which has not been read.
              emit({ kind: 'code', start: codeStart, end: pos - 1 }, boundary(pos - 1, 'code', codeStart));
            }
          } else if (c === STAR) {
            const codeStart = st.tokenStart;
            st.mode = 'blockComment';
            st.tokenStart = pos - 1;
            st.commentStack.push(pos - 1);
            if (codeStart < pos - 1) {
              emit({ kind: 'code', start: codeStart, end: pos - 1 }, boundary(pos - 1, 'code', codeStart));
            }
          } else if (c === QUOTE) {
            // The '/' was ordinary code; a string starts here. The true
            // state at `pos` still has the pending '/', so the boundary
            // records codeSlash: resuming re-decides the '/' from the next
            // character, exactly like the original scan did.
            const codeStart = st.tokenStart;
            st.mode = 'string';
            st.tokenStart = pos;
            st.escaped = false;
            if (codeStart < pos) {
              emit({ kind: 'code', start: codeStart, end: pos }, boundary(pos, 'codeSlash', codeStart));
            }
          } else {
            // The '/' was ordinary code; reprocess this char as code.
            st.mode = 'code';
            advance = false;
          }
          break;
        case 'string':
          if (st.escaped) {
            st.escaped = false;
          } else if (c === BACKSLASH) {
            st.escaped = true;
          } else if (c === QUOTE) {
            const start = st.tokenStart;
            st.mode = 'code';
            st.tokenStart = pos + 1;
            emit({ kind: 'string', start, end: pos + 1 }, boundary(pos + 1, 'code', pos + 1));
          }
          break;
        case 'lineComment':
          if (c === NEWLINE) {
            const start = st.tokenStart;
            st.mode = 'code';
            st.tokenStart = pos + 1;
            // The terminating newline is part of the token, so a token
            // boundary never depends on a character outside the token.
            emit({ kind: 'lineComment', start, end: pos + 1 }, boundary(pos + 1, 'code', pos + 1));
          }
          break;
        case 'blockComment':
          if (c === SLASH) {
            st.mode = 'blockCommentSlash';
          } else if (c === STAR) {
            st.mode = 'blockCommentStar';
          }
          break;
        case 'blockCommentSlash':
          if (c === STAR) {
            st.commentStack.push(pos - 1);
            st.mode = 'blockComment';
          } else if (c !== SLASH) {
            st.mode = 'blockComment';
          }
          // A second '/' stays in blockCommentSlash: `//*` nests at the 2nd '/'.
          break;
        case 'blockCommentStar':
          if (c === SLASH) {
            st.commentStack.pop();
            if (st.commentStack.length === 0) {
              const start = st.tokenStart;
              st.mode = 'code';
              st.tokenStart = pos + 1;
              emit({ kind: 'blockComment', start, end: pos + 1 }, boundary(pos + 1, 'code', pos + 1));
            } else {
              st.mode = 'blockComment';
            }
          } else if (c !== STAR) {
            st.mode = 'blockComment';
          }
          // A second '*' stays in blockCommentStar: `**/` closes at the 2nd '*'.
          break;
      }
      if (advance) i++;
    }
    st.offset = posBase + i;
  }

  /**
   * Flush the pending token at end of input and return the unterminated
   * constructs (empty when the input ended cleanly in code mode).
   */
  finish(emit: TokenSink): Unterminated[] {
    const st = this.state;
    const out: Unterminated[] = [];
    const end = boundary(st.offset, 'code', st.offset);
    switch (st.mode) {
      case 'code':
      case 'codeSlash':
        if (st.tokenStart < st.offset) {
          emit({ kind: 'code', start: st.tokenStart, end: st.offset }, end);
        }
        break;
      case 'string':
        emit({ kind: 'string', start: st.tokenStart, end: st.offset }, end);
        out.push({ kind: 'string', start: st.tokenStart });
        break;
      case 'lineComment':
        emit({ kind: 'lineComment', start: st.tokenStart, end: st.offset }, end);
        break;
      case 'blockComment':
      case 'blockCommentSlash':
      case 'blockCommentStar':
        emit({ kind: 'blockComment', start: st.tokenStart, end: st.offset }, end);
        for (const start of st.commentStack) {
          out.push({ kind: 'blockComment', start });
        }
        break;
    }
    st.mode = 'code';
    st.tokenStart = st.offset;
    st.escaped = false;
    st.commentStack.length = 0;
    return out;
  }
}
