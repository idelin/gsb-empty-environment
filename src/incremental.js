'use strict';

const { Lexer, MODE } = require('./lexer');

/**
 * Incremental lexer over a mutable document.
 *
 * `applyEdit(start, end, inserted)` takes coordinates in the *previous*
 * version of the document. Rescanning starts at the token boundary at or
 * before the edit (never a fixed number of lines) and stops as soon as both
 * the lexical state and a token boundary match the previous scan, so
 * unaffected tails are never re-tokenized.
 */
class IncrementalLexer {
  /** @param {string} [text] */
  constructor(text) {
    this.text = '';
    /** @type {Array<{type:string,start:number,end:number,text:string}>} */
    this.tokens = [];
    /** @type {Array<object>} */
    this.diagnostics = [];
    /** debug info about the most recent rescan */
    this.lastRescan = null;
    this.setText(text || '');
  }

  /** Replace the whole document and lex it from scratch. */
  setText(text) {
    const lexer = new Lexer();
    lexer.feed(text);
    lexer.end();
    this.text = text;
    this.tokens = lexer.tokens;
    this.diagnostics = lexer.diagnostics;
    this.lastRescan = { from: 0, scanned: text.length, stable: false };
  }

  /**
   * Apply an edit expressed in coordinates of the current (old) document:
   * replaces `this.text[start:end]` with `inserted`.
   * @param {number} start
   * @param {number} end
   * @param {string} inserted
   */
  applyEdit(start, end, inserted) {
    const oldText = this.text;
    const oldTokens = this.tokens;
    if (!(0 <= start && start <= end && end <= oldText.length)) {
      throw new RangeError(`invalid edit [${start}, ${end}) for length ${oldText.length}`);
    }
    const delta = inserted.length - (end - start);
    const newText = oldText.slice(0, start) + inserted + oldText.slice(end);
    const editNewEnd = start + inserted.length;

    // First old token that may be affected: the last token boundary at or
    // before the edit start. Lexical state there is always NORMAL because
    // strings/comments are emitted as single tokens.
    let i = 0;
    while (i < oldTokens.length && oldTokens[i].end < start) i++;
    const rescanOffset = i < oldTokens.length ? oldTokens[i].start : oldText.length;

    const lexer = new Lexer({ offset: rescanOffset });
    const newTokens = [];
    let stable = false;
    let stableIndex = -1;
    let scanned = 0;

    const CHUNK = 256;
    let pos = rescanOffset;
    while (pos < newText.length && !stable) {
      const next = Math.min(pos + CHUNK, newText.length);
      lexer.feed(newText.slice(pos, next));
      scanned = next - rescanOffset;
      pos = next;
      while (newTokens.length < lexer.tokens.length) {
        const t = lexer.tokens[newTokens.length];
        newTokens.push(t);
        // Stable once a freshly lexed token lies entirely past the edited
        // region and equals an old token (shifted by delta) that also lay
        // entirely past it. Token boundaries imply NORMAL lexical state.
        if (t.start >= editNewEnd && lexer.mode === MODE.NORMAL) {
          const oldStart = t.start - delta;
          for (let j = i; j < oldTokens.length; j++) {
            if (oldTokens[j].start < oldStart) continue;
            if (oldTokens[j].start > oldStart) break;
            if (
              oldTokens[j].start >= end &&
              oldTokens[j].end + delta === t.end &&
              oldTokens[j].type === t.type &&
              oldTokens[j].text === t.text
            ) {
              stable = true;
              stableIndex = j;
            }
            break;
          }
        }
        if (stable) break;
      }
    }

    if (stable) {
      // The matched token is represented by the shifted old tail; drop the
      // freshly lexed copy to avoid duplicating it.
      newTokens.pop();
      const tail = oldTokens.slice(stableIndex).map((t) => ({
        type: t.type,
        start: t.start + delta,
        end: t.end + delta,
        text: t.text,
      }));
      this.tokens = oldTokens.slice(0, i).concat(newTokens, tail);
      this.diagnostics = this.diagnostics.map((d) => shiftDiagnostic(d, end, delta));
    } else {
      lexer.end();
      while (newTokens.length < lexer.tokens.length) {
        newTokens.push(lexer.tokens[newTokens.length]);
      }
      scanned = newText.length - rescanOffset;
      this.tokens = oldTokens.slice(0, i).concat(newTokens);
      this.diagnostics = lexer.diagnostics.slice();
    }
    this.text = newText;
    this.lastRescan = { from: rescanOffset, scanned, stable };
  }
}

function shiftDiagnostic(d, editOldEnd, delta) {
  const shift = (n) => (n >= editOldEnd ? n + delta : n);
  if (d.kind === 'unterminated-comment') {
    return { kind: d.kind, start: shift(d.start), opens: d.opens.map(shift) };
  }
  return { kind: d.kind, start: shift(d.start) };
}

module.exports = { IncrementalLexer };
