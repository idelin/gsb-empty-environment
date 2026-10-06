'use strict';

/**
 * Streaming lexer for a TypeScript-like language.
 *
 * Token kinds: identifier, number, string (double-quoted, backslash escapes),
 * lineComment (// ...), blockComment (/* ... *\/, nestable), whitespace, punct.
 * Comment openers inside strings are inert, and vice versa.
 *
 * All offsets are absolute UTF-16 code-unit indices into the logical input
 * stream. Tokens tile the input contiguously: concatenating every token's
 * `text` reproduces the consumed input.
 */

const MODE = Object.freeze({
  NORMAL: 'normal',
  STRING: 'string',
  LINE_COMMENT: 'lineComment',
  BLOCK_COMMENT: 'blockComment',
});

function isIdStart(c) {
  return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c === '_' || c === '$';
}
function isDigit(c) {
  return c >= '0' && c <= '9';
}
function isWhitespace(c) {
  return c === ' ' || c === '\t' || c === '\n' || c === '\r' || c === '\v' || c === '\f';
}

class Lexer {
  /**
   * @param {{offset?: number}} [options] absolute offset of the first fed char
   */
  constructor(options) {
    /** absolute offset the next fed char will occupy */
    this.nextOffset = (options && options.offset) || 0;
    /** fed but not yet consumed chars (at most a 1-char lookahead) */
    this.buffer = '';
    this.mode = MODE.NORMAL;
    /** start offset of the pending token */
    this.tokenStart = 0;
    /** kind of the pending multi-char token, or null */
    this.pendingType = null;
    /** text accumulated for the pending token */
    this.pendingText = '';
    /** string state: previous char was a backslash */
    this.escaped = false;
    /** block-comment state: previous char was `*` */
    this.starPending = false;
    /** stack of absolute offsets of every unclosed nested `/*` */
    this.commentStack = [];
    /** @type {Array<{type:string,start:number,end:number,text:string}>} */
    this.tokens = [];
    /** @type {Array<object>} */
    this.diagnostics = [];
  }

  /**
   * Feed a chunk of input. Chunks may be split at any UTF-16 boundary;
   * the resulting token stream is identical to one-shot feeding.
   * @param {string} chunk
   */
  feed(chunk) {
    if (chunk.length > 0) {
      this.buffer += chunk;
      this.nextOffset += chunk.length;
    }
    this._consume(false);
  }

  /**
   * Signal end of input. Flushes any pending token and reports
   * unterminated constructs with their absolute start offsets.
   * @returns {Array<object>} diagnostics
   */
  end() {
    this._consume(true);
    this._flushPending();
    if (this.mode === MODE.STRING) {
      this.diagnostics.push({ kind: 'unterminated-string', start: this.tokenStart });
    } else if (this.mode === MODE.BLOCK_COMMENT) {
      this.diagnostics.push({
        kind: 'unterminated-comment',
        start: this.tokenStart,
        opens: this.commentStack.slice(),
      });
    }
    this.mode = MODE.NORMAL;
    return this.diagnostics;
  }

  /**
   * Capture a serializable pause checkpoint: unfinished string, nested
   * comment stack, pending token, lookahead buffer and source offset.
   * Restoring and resuming is indistinguishable from continuous scanning.
   */
  checkpoint() {
    return {
      version: 1,
      offset: this.nextOffset,
      mode: this.mode,
      tokenStart: this.tokenStart,
      pendingType: this.pendingType,
      pendingText: this.pendingText,
      escaped: this.escaped,
      starPending: this.starPending,
      commentStack: this.commentStack.slice(),
      buffer: this.buffer,
    };
  }

  /**
   * Create a lexer resumed from a checkpoint. Tokens emitted before the
   * checkpoint belong to the original lexer; concatenate `tokens` of both.
   * @param {ReturnType<Lexer['checkpoint']>} cp
   */
  static restore(cp) {
    if (!cp || cp.version !== 1) throw new Error('unsupported checkpoint');
    const lexer = new Lexer({ offset: cp.offset });
    lexer.mode = cp.mode;
    lexer.tokenStart = cp.tokenStart;
    lexer.pendingType = cp.pendingType;
    lexer.pendingText = cp.pendingText;
    lexer.escaped = cp.escaped;
    lexer.starPending = cp.starPending;
    lexer.commentStack = cp.commentStack.slice();
    lexer.buffer = cp.buffer;
    return lexer;
  }

  _take() {
    const c = this.buffer[0];
    this.buffer = this.buffer.slice(1);
    if (this.pendingType) this.pendingText += c;
    return c;
  }

  _emit(type, start, end, text) {
    this.tokens.push({ type, start, end, text });
  }

  _flushPending() {
    if (this.pendingType === null) return;
    const end = this.nextOffset - this.buffer.length;
    this._emit(this.pendingType, this.tokenStart, end, this.pendingText);
    this.pendingType = null;
    this.pendingText = '';
  }

  _consume(final) {
    while (this.buffer.length > 0) {
      const pos = this.nextOffset - this.buffer.length;
      const c = this.buffer[0];
      let progressed;
      if (this.mode === MODE.NORMAL) progressed = this._consumeNormal(c, pos, final);
      else if (this.mode === MODE.STRING) progressed = this._consumeString(c, pos);
      else if (this.mode === MODE.LINE_COMMENT) progressed = this._consumeLineComment(c, pos);
      else progressed = this._consumeBlockComment(c, pos, final);
      if (!progressed) return;
    }
  }

  _consumeNormal(c, pos, final) {
    if (c === '/') {
      if (this.buffer.length < 2 && !final) return false; // need lookahead
      this._flushPending();
      const next = this.buffer.length >= 2 ? this.buffer[1] : '';
      if (next === '/') {
        this.pendingType = 'lineComment';
        this.tokenStart = pos;
        this.pendingText = '';
        this._take();
        this._take();
        this.mode = MODE.LINE_COMMENT;
      } else if (next === '*') {
        this.pendingType = 'blockComment';
        this.tokenStart = pos;
        this.pendingText = '';
        this._take();
        this._take();
        this.commentStack.push(pos);
        this.mode = MODE.BLOCK_COMMENT;
      } else {
        this.buffer = this.buffer.slice(1);
        this._emit('punct', pos, pos + 1, '/');
      }
      return true;
    }
    if (c === '"') {
      this._flushPending();
      this.pendingType = 'string';
      this.tokenStart = pos;
      this.pendingText = '';
      this.escaped = false;
      this._take();
      this.mode = MODE.STRING;
      return true;
    }
    if (isIdStart(c)) {
      if (this.pendingType !== 'identifier') {
        this._flushPending();
        this.pendingType = 'identifier';
        this.tokenStart = pos;
        this.pendingText = '';
      }
      this._take();
      return true;
    }
    if (isDigit(c)) {
      if (this.pendingType === 'identifier') {
        this._take();
        return true;
      }
      if (this.pendingType !== 'number') {
        this._flushPending();
        this.pendingType = 'number';
        this.tokenStart = pos;
        this.pendingText = '';
      }
      this._take();
      return true;
    }
    if (c === '.' && this.pendingType === 'number') {
      if (this.buffer.length < 2 && !final) return false; // need lookahead
      if (isDigit(this.buffer[1] || '')) {
        this._take();
        return true;
      }
      // not a float: fall through to punctuation
    }
    if (isWhitespace(c)) {
      if (this.pendingType !== 'whitespace') {
        this._flushPending();
        this.pendingType = 'whitespace';
        this.tokenStart = pos;
        this.pendingText = '';
      }
      this._take();
      return true;
    }
    this._flushPending();
    this.buffer = this.buffer.slice(1);
    this._emit('punct', pos, pos + 1, c);
    return true;
  }

  _consumeString(c, pos) {
    if (this.escaped) {
      this.escaped = false;
      this._take();
      return true;
    }
    if (c === '\\') {
      this.escaped = true;
      this._take();
      return true;
    }
    if (c === '"') {
      this._take();
      this._emit('string', this.tokenStart, pos + 1, this.pendingText);
      this.pendingType = null;
      this.pendingText = '';
      this.mode = MODE.NORMAL;
      return true;
    }
    this._take();
    return true;
  }

  _consumeLineComment(c, pos) {
    if (c === '\n' || c === '\r') {
      this._emit('lineComment', this.tokenStart, pos, this.pendingText);
      this.pendingType = null;
      this.pendingText = '';
      this.mode = MODE.NORMAL;
      return true; // newline itself is re-consumed as whitespace
    }
    this._take();
    return true;
  }

  _consumeBlockComment(c, pos, final) {
    if (this.starPending) {
      if (c === '/') {
        this._take();
        this.starPending = false;
        this.commentStack.pop();
        if (this.commentStack.length === 0) {
          this._emit('blockComment', this.tokenStart, pos + 1, this.pendingText);
          this.pendingType = null;
          this.pendingText = '';
          this.mode = MODE.NORMAL;
        }
        return true;
      }
      if (c !== '*') this.starPending = false;
      this._take();
      return true;
    }
    if (c === '*') {
      this.starPending = true;
      this._take();
      return true;
    }
    if (c === '/') {
      if (this.buffer.length < 2 && !final) return false; // need lookahead
      if (this.buffer[1] === '*') {
        this._take();
        this._take();
        this.commentStack.push(pos);
        return true;
      }
      this._take();
      return true;
    }
    this._take();
    return true;
  }
}

module.exports = { Lexer, MODE };
