import { tokenize, KEYWORDS, type Token } from './lexer.ts';
import type { Program, Stmt, Expr, Block, Ident, ImportSpec } from './ast.ts';

export function parse(src: string, file: string): Program {
  const tokens = tokenize(src, file);
  let pos = 0;

  function peek(): Token {
    return tokens[pos];
  }
  function next(): Token {
    return tokens[pos++];
  }
  function error(msg: string): never {
    const t = tokens[Math.min(pos, tokens.length - 1)];
    throw new Error(`${file}: parse error at offset ${t.start}: ${msg}`);
  }
  function isPunct(v: string): boolean {
    const t = peek();
    return t.kind === 'punct' && t.value === v;
  }
  function atKeyword(kw: string): boolean {
    const t = peek();
    return t.kind === 'ident' && t.value === kw;
  }
  function expectPunct(v: string): Token {
    const t = next();
    if (t.kind !== 'punct' || t.value !== v) error(`expected '${v}', found '${t.value}'`);
    return t;
  }
  function parseIdent(): Ident {
    const t = next();
    if (t.kind !== 'ident' || KEYWORDS.has(t.value)) error(`expected identifier, found '${t.value}'`);
    return { kind: 'Ident', name: t.value, start: t.start, end: t.end };
  }
  function parseBlock(): Block {
    const open = expectPunct('{');
    const body: Stmt[] = [];
    while (!isPunct('}')) {
      if (peek().kind === 'eof') error('unterminated block');
      body.push(parseStmt());
    }
    const close = expectPunct('}');
    return { kind: 'Block', body, start: open.start, end: close.end };
  }
  function parseStmt(): Stmt {
    if (atKeyword('let')) {
      const kw = next();
      const id = parseIdent();
      expectPunct('=');
      const init = parseExpr();
      const semi = expectPunct(';');
      return { kind: 'Let', id, init, start: kw.start, end: semi.end };
    }
    if (atKeyword('fn')) {
      const kw = next();
      const id = parseIdent();
      expectPunct('(');
      const params: Ident[] = [];
      if (!isPunct(')')) {
        params.push(parseIdent());
        while (isPunct(',')) {
          next();
          params.push(parseIdent());
        }
      }
      expectPunct(')');
      const body = parseBlock();
      return { kind: 'Fn', id, params, body, start: kw.start, end: body.end };
    }
    if (atKeyword('import')) {
      const kw = next();
      expectPunct('{');
      const specs: ImportSpec[] = [];
      for (;;) {
        const imported = parseIdent();
        let local = imported;
        if (atKeyword('as')) {
          next();
          local = parseIdent();
        }
        specs.push({ imported, local });
        if (isPunct(',')) {
          next();
          continue;
        }
        break;
      }
      expectPunct('}');
      if (!atKeyword('from')) error(`expected 'from'`);
      next();
      const str = next();
      if (str.kind !== 'string') error('expected module path string');
      const semi = expectPunct(';');
      return { kind: 'Import', specs, source: str.value, start: kw.start, end: semi.end };
    }
    if (atKeyword('return')) {
      const kw = next();
      const arg = parseExpr();
      const semi = expectPunct(';');
      return { kind: 'Return', arg, start: kw.start, end: semi.end };
    }
    if (isPunct('{')) return parseBlock();
    const expr = parseExpr();
    const semi = expectPunct(';');
    return { kind: 'ExprStmt', expr, start: expr.start, end: semi.end };
  }
  function parseExpr(): Expr {
    const left = parseAdditive();
    if (isPunct('=')) {
      if (left.kind !== 'Ident') error('invalid assignment target');
      next();
      const value = parseExpr();
      return { kind: 'Assign', target: left, value, start: left.start, end: value.end };
    }
    return left;
  }
  function parseAdditive(): Expr {
    let left = parsePrimary();
    while (isPunct('+')) {
      next();
      const right = parsePrimary();
      left = { kind: 'Binary', op: '+', left, right, start: left.start, end: right.end };
    }
    return left;
  }
  function parsePrimary(): Expr {
    const t = peek();
    if (t.kind === 'number') {
      next();
      return { kind: 'Num', start: t.start, end: t.end };
    }
    if (t.kind === 'string') {
      next();
      return { kind: 'Str', value: t.value, start: t.start, end: t.end };
    }
    if (t.kind === 'ident' && !KEYWORDS.has(t.value)) {
      const id = parseIdent();
      if (isPunct('(')) {
        next();
        const args: Expr[] = [];
        if (!isPunct(')')) {
          args.push(parseExpr());
          while (isPunct(',')) {
            next();
            args.push(parseExpr());
          }
        }
        const close = expectPunct(')');
        return { kind: 'Call', callee: id, args, start: id.start, end: close.end };
      }
      return id;
    }
    if (isPunct('(')) {
      next();
      const e = parseExpr();
      expectPunct(')');
      return e;
    }
    error(`unexpected token '${t.value}'`);
  }

  const body: Stmt[] = [];
  while (peek().kind !== 'eof') body.push(parseStmt());
  return { kind: 'Program', body, start: 0, end: src.length };
}
