import { tokenize, type Token } from './lexer.ts';
import type {
  Block,
  Expr,
  ExprStmt,
  FnDecl,
  Ident,
  ImportDecl,
  ImportSpec,
  LetDecl,
  Program,
  ReturnStmt,
  Stmt,
} from './ast.ts';

export class ParseError extends Error {
  pos: number;
  constructor(message: string, pos: number) {
    super(message);
    this.name = 'ParseError';
    this.pos = pos;
  }
}

export function parse(src: string): Program {
  const tokens = tokenize(src);
  let pos = 0;
  const idents: Ident[] = [];

  function peek(): Token {
    return tokens[pos];
  }
  function next(): Token {
    return tokens[pos++];
  }
  function expect(value: string): Token {
    const t = next();
    if (t.value !== value) {
      throw new ParseError(`expected '${value}' but found '${t.value || t.type}'`, t.start);
    }
    return t;
  }
  function makeIdent(name: string, start: number, end: number): Ident {
    const ident: Ident = { type: 'Ident', name, id: idents.length, start, end };
    idents.push(ident);
    return ident;
  }
  function expectIdent(): Ident {
    const t = next();
    if (t.type !== 'ident') {
      throw new ParseError(`expected identifier but found '${t.value || t.type}'`, t.start);
    }
    return makeIdent(t.value, t.start, t.end);
  }

  function parseProgram(): Program {
    const body: Stmt[] = [];
    while (peek().type !== 'eof') body.push(parseStmt());
    return { type: 'Program', body, idents, start: 0, end: src.length };
  }

  function parseStmt(): Stmt {
    const t = peek();
    if (t.type === 'keyword') {
      switch (t.value) {
        case 'let':
          return parseLet(false);
        case 'fn':
          return parseFn(false);
        case 'import':
          return parseImport();
        case 'return':
          return parseReturn();
        case 'export': {
          next();
          const k = peek();
          if (k.type === 'keyword' && k.value === 'let') return parseLet(true);
          if (k.type === 'keyword' && k.value === 'fn') return parseFn(true);
          throw new ParseError(`expected 'let' or 'fn' after 'export'`, k.start);
        }
      }
    }
    if (t.value === '{') return parseBlock();
    return parseExprStmt();
  }

  function parseLet(exported: boolean): LetDecl {
    const start = expect('let').start;
    const name = expectIdent();
    expect('=');
    const init = parseExpr();
    const end = expect(';').end;
    return { type: 'LetDecl', exported, name, init, start, end };
  }

  function parseFn(exported: boolean): FnDecl {
    const start = expect('fn').start;
    const name = expectIdent();
    expect('(');
    const params: Ident[] = [];
    if (peek().value !== ')') {
      while (true) {
        params.push(expectIdent());
        if (peek().value !== ',') break;
        next();
      }
    }
    expect(')');
    const body = parseBlock();
    return { type: 'FnDecl', exported, name, params, body, start, end: body.end };
  }

  function parseImport(): ImportDecl {
    const start = expect('import').start;
    expect('{');
    const specs: ImportSpec[] = [];
    if (peek().value !== '}') {
      while (true) {
        const imported = expectIdent();
        let local: Ident | null = null;
        if (peek().value === 'as') {
          next();
          local = expectIdent();
        }
        specs.push({ imported, local });
        if (peek().value !== ',') break;
        next();
      }
    }
    expect('}');
    expect('from');
    const srcTok = next();
    if (srcTok.type !== 'string') {
      throw new ParseError('expected module path string after from', srcTok.start);
    }
    const end = expect(';').end;
    return {
      type: 'ImportDecl',
      specs,
      source: srcTok.value,
      sourceStart: srcTok.start,
      sourceEnd: srcTok.end,
      start,
      end,
    };
  }

  function parseBlock(): Block {
    const start = expect('{').start;
    const body: Stmt[] = [];
    while (peek().value !== '}') {
      if (peek().type === 'eof') throw new ParseError('unterminated block', start);
      body.push(parseStmt());
    }
    const end = expect('}').end;
    return { type: 'Block', body, start, end };
  }

  function parseReturn(): ReturnStmt {
    const start = expect('return').start;
    let arg: Expr | null = null;
    if (peek().value !== ';') arg = parseExpr();
    const end = expect(';').end;
    return { type: 'ReturnStmt', arg, start, end };
  }

  function parseExprStmt(): ExprStmt {
    const expr = parseExpr();
    const end = expect(';').end;
    return { type: 'ExprStmt', expr, start: expr.start, end };
  }

  function parseExpr(): Expr {
    return parseAdd();
  }

  function parseAdd(): Expr {
    let left = parseMul();
    while (peek().value === '+' || peek().value === '-') {
      const op = next().value;
      const right = parseMul();
      left = { type: 'BinExpr', op, left, right, start: left.start, end: right.end };
    }
    return left;
  }

  function parseMul(): Expr {
    let left = parsePostfix();
    while (peek().value === '*' || peek().value === '/') {
      const op = next().value;
      const right = parsePostfix();
      left = { type: 'BinExpr', op, left, right, start: left.start, end: right.end };
    }
    return left;
  }

  function parsePostfix(): Expr {
    let expr = parsePrimary();
    while (peek().value === '(') {
      next();
      const args: Expr[] = [];
      if (peek().value !== ')') {
        while (true) {
          args.push(parseExpr());
          if (peek().value !== ',') break;
          next();
        }
      }
      const end = expect(')').end;
      expr = { type: 'CallExpr', callee: expr, args, start: expr.start, end };
    }
    return expr;
  }

  function parsePrimary(): Expr {
    const t = next();
    if (t.type === 'number') {
      return { type: 'NumLit', value: Number(t.value), start: t.start, end: t.end };
    }
    if (t.type === 'string') {
      return { type: 'StrLit', value: t.value, start: t.start, end: t.end };
    }
    if (t.type === 'ident') {
      return makeIdent(t.value, t.start, t.end);
    }
    if (t.value === '(') {
      const expr = parseExpr();
      expect(')');
      return expr;
    }
    throw new ParseError(`unexpected token '${t.value || t.type}'`, t.start);
  }

  return parseProgram();
}
