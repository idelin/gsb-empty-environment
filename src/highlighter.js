/**
 * Incremental syntax highlighter.
 *
 * The document is stored as an array of lines. For every line we keep:
 *   - inputState:  lexical state at the start of the line
 *   - outputState: lexical state at the end of the line
 *   - tokens:      tokens with line-relative UTF-16 ranges
 *
 * After a line is edited, affected lines are re-lexed forward starting at
 * the edited line. Re-lexing stops as soon as a line produces exactly the
 * same tokens and the same output state as stored, because then every
 * following line is guaranteed to be unchanged. There is no fixed line
 * count limit: if a state change propagates to the end of the document
 * (e.g. an unclosed block comment), every affected line is re-lexed.
 *
 * Tokens are stored line-relative so that pure offset shifts (caused by
 * length changes in earlier lines) do not defeat stabilization. Absolute
 * UTF-16 ranges are computed on demand by getTokens().
 */

import { lexLine, tokensEqual, State } from './lexer.js';

/**
 * @typedef {Object} AbsToken
 * @property {number} start - Absolute UTF-16 offset in the document (inclusive).
 * @property {number} end - Absolute UTF-16 offset in the document (exclusive).
 * @property {string} type
 * @property {number} line - Zero-based line index.
 */

export class IncrementalHighlighter {
  /** @param {string} text Full document text. */
  constructor(text) {
    this.setText(text);
  }

  /** Replace the whole document and lex it from scratch. */
  setText(text) {
    this.lines = text.split('\n');
    /** @type {{inputState: number, outputState: number, tokens: import('./lexer.js').Token[]}[]} */
    this.lineStates = new Array(this.lines.length);
    /** Absolute UTF-16 offset of the first character of each line. */
    this.lineStarts = new Array(this.lines.length);
    let offset = 0;
    let state = State.Code;
    for (let i = 0; i < this.lines.length; i++) {
      this.lineStarts[i] = offset;
      const { tokens, outputState } = lexLine(this.lines[i], state);
      this.lineStates[i] = { inputState: state, outputState, tokens };
      state = outputState;
      offset += this.lines[i].length + 1; // +1 for the '\n'
    }
    this.lastRelexedLines = this.lines.length;
  }

  /** @returns {string} Current document text. */
  getText() {
    return this.lines.join('\n');
  }

  /**
   * Replace a single line and re-lex forward until tokens and lexical
   * state stabilize.
   *
   * @param {number} index Zero-based line index.
   * @param {string} newText New content of the line (must not contain '\n').
   * @returns {number} Number of lines that had to be re-lexed and changed.
   */
  editLine(index, newText) {
    if (index < 0 || index >= this.lines.length) {
      throw new RangeError(`line index ${index} out of range`);
    }
    if (newText.includes('\n')) {
      throw new Error('editLine replaces exactly one line; newText must not contain a newline');
    }
    this.lines[index] = newText;

    // Absolute offsets of all following lines may have shifted.
    let offset = this.lineStarts[index];
    for (let i = index; i < this.lines.length; i++) {
      this.lineStarts[i] = offset;
      offset += this.lines[i].length + 1;
    }

    let state = index === 0 ? State.Code : this.lineStates[index - 1].outputState;
    let relexed = 0;
    for (let i = index; i < this.lines.length; i++) {
      const { tokens, outputState } = lexLine(this.lines[i], state);
      const prev = this.lineStates[i];
      if (prev.inputState === state && prev.outputState === outputState &&
          tokensEqual(prev.tokens, tokens)) {
        break; // line and everything after it is unchanged
      }
      this.lineStates[i] = { inputState: state, outputState, tokens };
      relexed += 1;
      state = outputState;
    }
    this.lastRelexedLines = relexed;
    return relexed;
  }

  /**
   * Lexical state at the start/end of a line, for inspection and testing.
   * @returns {{inputState: number, outputState: number}}
   */
  getLineState(index) {
    const { inputState, outputState } = this.lineStates[index];
    return { inputState, outputState };
  }

  /**
   * All tokens with absolute UTF-16 ranges in the document.
   * @returns {AbsToken[]}
   */
  getTokens() {
    const out = [];
    for (let i = 0; i < this.lines.length; i++) {
      const base = this.lineStarts[i];
      for (const t of this.lineStates[i].tokens) {
        out.push({ start: base + t.start, end: base + t.end, type: t.type, line: i });
      }
    }
    return out;
  }
}

/**
 * Full (non-incremental) highlight of a document. Used as the ground truth
 * to verify incremental results.
 * @param {string} text
 * @returns {AbsToken[]}
 */
export function fullHighlight(text) {
  return new IncrementalHighlighter(text).getTokens();
}
