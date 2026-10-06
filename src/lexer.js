/**
 * Line-oriented lexer for a TypeScript-like language.
 *
 * Multi-line constructs (block comments and template literals) are modeled
 * with an explicit lexical state carried across lines. Each line is lexed
 * with an input state and produces an output state, which becomes the input
 * state of the next line.
 *
 * All offsets are UTF-16 code unit indices (i.e. JavaScript string indices),
 * relative to the start of the line.
 *
 * Simplification: template literal `${ ... }` interpolation is not parsed;
 * a template literal runs until the next unescaped backtick.
 */

/** @enum {number} Lexical state carried across line boundaries. */
export const State = Object.freeze({
  Code: 0,
  BlockComment: 1,
  Template: 2,
});

/**
 * @typedef {Object} Token
 * @property {number} start - UTF-16 offset of the first character (inclusive).
 * @property {number} end - UTF-16 offset one past the last character (exclusive).
 * @property {string} type - One of: keyword, identifier, number, string,
 *   template, comment, punctuation.
 */

const KEYWORDS = new Set([
  'abstract', 'any', 'as', 'async', 'await', 'boolean', 'break', 'case',
  'catch', 'class', 'const', 'continue', 'debugger', 'declare', 'default',
  'delete', 'do', 'else', 'enum', 'export', 'extends', 'false', 'finally',
  'for', 'from', 'function', 'if', 'implements', 'import', 'in',
  'instanceof', 'interface', 'let', 'never', 'new', 'null', 'number',
  'of', 'private', 'protected', 'public', 'readonly', 'return', 'static',
  'string', 'super', 'switch', 'this', 'throw', 'true', 'try', 'type',
  'typeof', 'undefined', 'var', 'void', 'while', 'yield',
]);

const NUMBER_RE = /^(?:0[xX][0-9a-fA-F_]+|0[bB][01_]+|0[oO][0-7_]+|[0-9][0-9_]*(?:\.[0-9_]*)?(?:[eE][+-]?[0-9]+)?n?)/;
const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*/;

function isSpace(ch) {
  return ch === ' ' || ch === '\t' || ch === '\r';
}

/** End offset (exclusive) of a quoted string starting at `start`, or line end. */
function findStringEnd(text, start, quote) {
  let j = start + 1;
  while (j < text.length) {
    const ch = text[j];
    if (ch === '\\') {
      j += 2;
    } else if (ch === quote) {
      return j + 1;
    } else {
      j += 1;
    }
  }
  return text.length; // unterminated: ends at line end, state stays Code
}

/** Index of the next unescaped backtick at or after `from`, or -1. */
function findTemplateEnd(text, from) {
  let j = from;
  while (j < text.length) {
    const ch = text[j];
    if (ch === '\\') {
      j += 2;
    } else if (ch === '`') {
      return j;
    } else {
      j += 1;
    }
  }
  return -1;
}

/**
 * Lex a single line.
 *
 * @param {string} text - Line content without the trailing newline.
 * @param {number} inputState - Lexical state at the start of the line.
 * @returns {{ tokens: Token[], outputState: number }}
 */
export function lexLine(text, inputState) {
  /** @type {Token[]} */
  const tokens = [];
  let state = inputState;
  let i = 0;
  const n = text.length;

  while (i < n) {
    if (state === State.BlockComment) {
      const close = text.indexOf('*/', i);
      if (close === -1) {
        tokens.push({ start: i, end: n, type: 'comment' });
        i = n;
      } else {
        tokens.push({ start: i, end: close + 2, type: 'comment' });
        i = close + 2;
        state = State.Code;
      }
      continue;
    }

    if (state === State.Template) {
      const close = findTemplateEnd(text, i);
      if (close === -1) {
        tokens.push({ start: i, end: n, type: 'template' });
        i = n;
      } else {
        tokens.push({ start: i, end: close + 1, type: 'template' });
        i = close + 1;
        state = State.Code;
      }
      continue;
    }

    const ch = text[i];

    if (isSpace(ch)) {
      i += 1;
      continue;
    }

    if (ch === '/' && text[i + 1] === '/') {
      tokens.push({ start: i, end: n, type: 'comment' });
      i = n;
      continue;
    }

    if (ch === '/' && text[i + 1] === '*') {
      const close = text.indexOf('*/', i + 2);
      if (close === -1) {
        tokens.push({ start: i, end: n, type: 'comment' });
        i = n;
        state = State.BlockComment;
      } else {
        tokens.push({ start: i, end: close + 2, type: 'comment' });
        i = close + 2;
      }
      continue;
    }

    if (ch === '"' || ch === "'") {
      const end = findStringEnd(text, i, ch);
      tokens.push({ start: i, end, type: 'string' });
      i = end;
      continue;
    }

    if (ch === '`') {
      const close = findTemplateEnd(text, i + 1);
      if (close === -1) {
        tokens.push({ start: i, end: n, type: 'template' });
        i = n;
        state = State.Template;
      } else {
        tokens.push({ start: i, end: close + 1, type: 'template' });
        i = close + 1;
      }
      continue;
    }

    if (/[0-9]/.test(ch)) {
      const numMatch = NUMBER_RE.exec(text.slice(i));
      const len = numMatch ? numMatch[0].length : 1;
      tokens.push({ start: i, end: i + len, type: 'number' });
      i += len;
      continue;
    }

    const identMatch = IDENT_RE.exec(text.slice(i));
    if (identMatch) {
      const word = identMatch[0];
      tokens.push({
        start: i,
        end: i + word.length,
        type: KEYWORDS.has(word) ? 'keyword' : 'identifier',
      });
      i += word.length;
      continue;
    }

    tokens.push({ start: i, end: i + 1, type: 'punctuation' });
    i += 1;
  }

  return { tokens, outputState: state };
}

/** Structural equality for token arrays (line-relative ranges). */
export function tokensEqual(a, b) {
  if (a.length !== b.length) return false;
  for (let k = 0; k < a.length; k++) {
    if (a[k].start !== b[k].start || a[k].end !== b[k].end || a[k].type !== b[k].type) {
      return false;
    }
  }
  return true;
}
