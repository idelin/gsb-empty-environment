// Lexer: produces tokens with source ranges and attaches line comments.
//
// Comment attachment rules:
//   - a comment on the same line after code attaches as the *trailing*
//     comment of the last token on that line;
//   - a comment on its own line attaches as a *leading* comment of the
//     next token;
//   - comments with no following token (end of file) are kept in `tail`.
//
// Note: `//` always starts a comment, so write division with spaces or
// before a non-`/` token (the printer always emits `a / b` with spaces).

export class ParseError extends Error {
  constructor(pos, expected, src) {
    const { line, col } = lineCol(src, pos);
    super(`parse error at line ${line}, col ${col} (offset ${pos}): expected ${expected.join(', ')}`);
    this.name = 'ParseError';
    this.pos = pos;
    this.line = line;
    this.col = col;
    this.expected = expected;
  }
}

export function lineCol(src, pos) {
  let line = 1, col = 1;
  for (let i = 0; i < pos && i < src.length; i++) {
    if (src[i] === '\n') { line++; col = 1; } else { col++; }
  }
  return { line, col };
}

const PUNCTS = new Set(['+', '-', '*', '/', '(', ')', ',']);

export function lex(src) {
  const tokens = [];
  const comments = [];
  let i = 0, line = 1, col = 1;

  const advance = (n) => {
    for (let k = 0; k < n; k++) {
      if (src[i] === '\n') { line++; col = 1; } else { col++; }
      i++;
    }
  };

  while (i < src.length) {
    const ch = src[i];
    if (ch === ' ' || ch === '\t' || ch === '\r' || ch === '\n') { advance(1); continue; }

    if (ch === '/' && src[i + 1] === '/') {
      const start = i, startLine = line, startCol = col;
      while (i < src.length && src[i] !== '\n') advance(1);
      comments.push({ text: src.slice(start, i), start, end: i, line: startLine, col: startCol });
      continue;
    }

    if (/[0-9]/.test(ch)) {
      const start = i, startLine = line, startCol = col;
      while (i < src.length && /[0-9]/.test(src[i])) advance(1);
      tokens.push({
        type: 'int', value: src.slice(start, i),
        start, end: i, line: startLine, col: startCol,
        leading: [], trailing: null,
      });
      continue;
    }

    if (/[A-Za-z_]/.test(ch)) {
      const start = i, startLine = line, startCol = col;
      while (i < src.length && /[A-Za-z0-9_]/.test(src[i])) advance(1);
      tokens.push({
        type: 'ident', value: src.slice(start, i),
        start, end: i, line: startLine, col: startCol,
        leading: [], trailing: null,
      });
      continue;
    }

    if (PUNCTS.has(ch)) {
      tokens.push({
        type: 'punct', value: ch,
        start: i, end: i + 1, line, col,
        leading: [], trailing: null,
      });
      advance(1);
      continue;
    }

    throw new ParseError(i, ['identifier', 'integer', "operator/paren ('+','-','*','/','(',')',',')", 'comment'], src);
  }

  // Attach comments to tokens per the rules above.
  const tail = [];
  let pending = [];
  let ti = 0;
  for (const c of comments) {
    while (ti < tokens.length && tokens[ti].end <= c.start) ti++;
    const prev = ti > 0 ? tokens[ti - 1] : null;
    if (prev && prev.line === c.line && prev.end <= c.start) {
      prev.trailing = c; // at most one: a line comment runs to end of line
    } else {
      pending.push(c);
    }
    // Standalone comments attach to the next token after them, if any.
    if (pending.length && ti < tokens.length) {
      tokens[ti].leading.push(...pending);
      pending = [];
    }
  }
  tail.push(...pending);

  return { tokens, tail };
}
