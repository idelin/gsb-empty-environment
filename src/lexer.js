'use strict';

// A ParseError carries the earliest position where analysis cannot continue
// plus the set of things that were expected there.
class ParseError extends Error {
  constructor(pos, expected) {
    const list = Array.from(expected).join(', ');
    super(`parse error at offset ${pos}: expected one of [${list}]`);
    this.name = 'ParseError';
    this.pos = pos;
    this.expected = Array.from(expected);
  }
}

const PUNCT = { '(': 'lparen', ')': 'rparen', ',': 'comma' };
const OPS = { '+': 'op', '-': 'op', '*': 'op', '/': 'op' };

function isIdentStart(ch) {
  return /[A-Za-z_]/.test(ch);
}
function isIdentPart(ch) {
  return /[A-Za-z0-9_]/.test(ch);
}
function isDigit(ch) {
  return /[0-9]/.test(ch);
}

// Tokenize source into tokens. Every token records its source span
// [start, end) and its line. Comments are attached as trivia:
//   - a comment on the same line as a preceding token becomes that token's
//     `trailing` trivia (the token is the last token of its line, since a
//     line comment runs to end-of-line);
//   - a comment on its own line becomes `leading` trivia of the next token;
//   - comments with no following token stay as `leading` trivia of the
//     synthetic EOF token, i.e. they are preserved at the file tail.
function tokenize(src) {
  const tokens = [];
  let pending = [];
  let i = 0;
  let line = 1;

  const lastTokenOnLine = (ln) => {
    if (tokens.length === 0) return null;
    const t = tokens[tokens.length - 1];
    return t.line === ln ? t : null;
  };

  while (i < src.length) {
    const ch = src[i];
    if (ch === ' ' || ch === '\t' || ch === '\r') {
      i++;
      continue;
    }
    if (ch === '\n') {
      line++;
      i++;
      continue;
    }
    if (ch === '#') {
      const start = i;
      while (i < src.length && src[i] !== '\n') i++;
      const comment = { text: src.slice(start, i), start, end: i, line };
      const prev = lastTokenOnLine(line);
      if (prev) prev.trailing.push(comment);
      else pending.push(comment);
      continue;
    }

    const start = i;
    let type;
    if (isIdentStart(ch)) {
      i++;
      while (i < src.length && isIdentPart(src[i])) i++;
      type = 'ident';
    } else if (isDigit(ch)) {
      i++;
      while (i < src.length && isDigit(src[i])) i++;
      type = 'int';
    } else if (PUNCT[ch]) {
      i++;
      type = PUNCT[ch];
    } else if (OPS[ch]) {
      i++;
      type = 'op';
    } else {
      throw new ParseError(start, ['identifier', 'integer', '(', ')', ',', '+', '-', '*', '/']);
    }

    tokens.push({
      type,
      text: src.slice(start, i),
      start,
      end: i,
      line,
      leading: pending,
      trailing: [],
    });
    pending = [];
  }

  tokens.push({
    type: 'eof',
    text: '',
    start: src.length,
    end: src.length,
    line,
    leading: pending,
    trailing: [],
  });
  return tokens;
}

module.exports = { tokenize, ParseError };
