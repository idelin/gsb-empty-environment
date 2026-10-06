import { tokenize } from './lexer.ts';
import type { Token } from './lexer.ts';
import type {
  AssignExpr,
  BinaryExpr,
  BlockStmt,
  BoolLit,
  CallExpr,
  Expr,
  ExprStmt,
  FnDeclStmt,
  Ident,
  IfStmt,
  LetStmt,
  NullLit,
  NumLit,
  Program,
  ReturnStmt,
  Stmt,
  StrLit,
  UnaryExpr,
  WhileStmt,
} from './ast.ts';

export class ParseError extends Error {}

class Parser {
  tokens: Token[];
  pos = 0;
  nextId = 1;

  constructor(source: string) {
    this.tokens = tokenize(source);
  }

  peek(): Token {
    return this.tokens[this.pos];
  }

  prev(): Token {
    return this.tokens[this.pos - 1];
  }

  at(type: Token['type'], value?: string): boolean {
    const t = this.peek();
    return t.type === type && (value === undefined || t.value === value);
  }

  match(type: Token['type'], value?: string): Token | null {
    if (this.at(type, value)) {
      this.pos++;
      return this.prev();
    }
    return null;
  }

  expect(type: Token['type'], value?: string): Token {
    const t = this.match(type, value);
    if (!t) {
      const got = this.peek();
      const want = value !== undefined ? `'${value}'` : type;
      throw new ParseError(
        `expected ${want} but found '${got.value || got.type}' at line ${got.line}`,
      );
    }
    return t;
  }

  base<T extends { kind: string }>(node: T, startTok: Token, endTok: Token): T & { id: number; start: number; end: number; line: number } {
    return Object.assign(node, {
      id: this.nextId++,
      start: startTok.start,
      end: endTok.end,
      line: startTok.line,
    });
  }

  parseProgram(): Program {
    const body: Stmt[] = [];
    const first = this.peek();
    while (!this.at('eof')) body.push(this.parseStmt());
    const last = this.peek();
    return this.base({ kind: 'Program', body }, first, last);
  }

  parseStmt(): Stmt {
    const t = this.peek();
    if (this.at('keyword', 'let')) return this.parseLet();
    if (this.at('keyword', 'if')) return this.parseIf();
    if (this.at('keyword', 'while')) return this.parseWhile();
    if (this.at('keyword', 'fn')) return this.parseFnDecl();
    if (this.at('keyword', 'return')) return this.parseReturn();
    if (this.at('punct', '{')) return this.parseBlock();
    const expr = this.parseExpr();
    const semi = this.expect('punct', ';');
    return this.base<ExprStmt>({ kind: 'ExprStmt', expr }, t, semi);
  }

  parseLet(): LetStmt {
    const kw = this.expect('keyword', 'let');
    const name = this.expect('ident').value;
    this.expect('op', '=');
    const init = this.parseExpr();
    const semi = this.expect('punct', ';');
    return this.base<LetStmt>({ kind: 'Let', name, init }, kw, semi);
  }

  parseIf(): IfStmt {
    const kw = this.expect('keyword', 'if');
    this.expect('punct', '(');
    const cond = this.parseExpr();
    this.expect('punct', ')');
    const thenBranch = this.parseStmt();
    let elseBranch: Stmt | null = null;
    let endTok = this.prev();
    if (this.match('keyword', 'else')) {
      elseBranch = this.parseStmt();
      endTok = this.prev();
    }
    return this.base<IfStmt>({ kind: 'If', cond, thenBranch, elseBranch }, kw, endTok);
  }

  parseWhile(): WhileStmt {
    const kw = this.expect('keyword', 'while');
    this.expect('punct', '(');
    const cond = this.parseExpr();
    this.expect('punct', ')');
    const body = this.parseStmt();
    return this.base<WhileStmt>({ kind: 'While', cond, body }, kw, this.prev());
  }

  parseFnDecl(): FnDeclStmt {
    const kw = this.expect('keyword', 'fn');
    const name = this.expect('ident').value;
    this.expect('punct', '(');
    const params: string[] = [];
    if (!this.at('punct', ')')) {
      params.push(this.expect('ident').value);
      while (this.match('punct', ',')) params.push(this.expect('ident').value);
    }
    this.expect('punct', ')');
    const body = this.parseBlock();
    return this.base<FnDeclStmt>({ kind: 'FnDecl', name, params, body }, kw, this.prev());
  }

  parseReturn(): ReturnStmt {
    const kw = this.expect('keyword', 'return');
    let value: Expr | null = null;
    if (!this.at('punct', ';')) value = this.parseExpr();
    const semi = this.expect('punct', ';');
    return this.base<ReturnStmt>({ kind: 'Return', value }, kw, semi);
  }

  parseBlock(): BlockStmt {
    const open = this.expect('punct', '{');
    const body: Stmt[] = [];
    while (!this.at('punct', '}')) {
      if (this.at('eof')) throw new ParseError(`unterminated block starting at line ${open.line}`);
      body.push(this.parseStmt());
    }
    const close = this.expect('punct', '}');
    return this.base<BlockStmt>({ kind: 'Block', body }, open, close);
  }

  parseExpr(): Expr {
    return this.parseAssign();
  }

  parseAssign(): Expr {
    const left = this.parseOr();
    if (this.at('op', '=')) {
      if (left.kind !== 'Ident') {
        throw new ParseError(`invalid assignment target at line ${left.line}`);
      }
      const eq = this.expect('op', '=');
      const value = this.parseAssign();
      return this.base<AssignExpr>({ kind: 'Assign', name: (left as Ident).name, value }, eq, this.prev());
    }
    return left;
  }

  parseOr(): Expr {
    let left = this.parseAnd();
    while (this.at('op', '||')) {
      const op = this.expect('op', '||');
      const right = this.parseAnd();
      left = this.base<BinaryExpr>({ kind: 'Binary', op: '||', left, right }, op, this.prev());
    }
    return left;
  }

  parseAnd(): Expr {
    let left = this.parseEquality();
    while (this.at('op', '&&')) {
      const op = this.expect('op', '&&');
      const right = this.parseEquality();
      left = this.base<BinaryExpr>({ kind: 'Binary', op: '&&', left, right }, op, this.prev());
    }
    return left;
  }

  parseEquality(): Expr {
    let left = this.parseComparison();
    while (this.at('op', '==') || this.at('op', '!=')) {
      const op = this.expect('op');
      const right = this.parseComparison();
      left = this.base<BinaryExpr>({ kind: 'Binary', op: op.value, left, right }, op, this.prev());
    }
    return left;
  }

  parseComparison(): Expr {
    let left = this.parseAdditive();
    while (
      this.at('op', '<') ||
      this.at('op', '<=') ||
      this.at('op', '>') ||
      this.at('op', '>=')
    ) {
      const op = this.expect('op');
      const right = this.parseAdditive();
      left = this.base<BinaryExpr>({ kind: 'Binary', op: op.value, left, right }, op, this.prev());
    }
    return left;
  }

  parseAdditive(): Expr {
    let left = this.parseMultiplicative();
    while (this.at('op', '+') || this.at('op', '-')) {
      const op = this.expect('op');
      const right = this.parseMultiplicative();
      left = this.base<BinaryExpr>({ kind: 'Binary', op: op.value, left, right }, op, this.prev());
    }
    return left;
  }

  parseMultiplicative(): Expr {
    let left = this.parseUnary();
    while (this.at('op', '*') || this.at('op', '/') || this.at('op', '%')) {
      const op = this.expect('op');
      const right = this.parseUnary();
      left = this.base<BinaryExpr>({ kind: 'Binary', op: op.value, left, right }, op, this.prev());
    }
    return left;
  }

  parseUnary(): Expr {
    if (this.at('op', '!') || this.at('op', '-')) {
      const op = this.expect('op');
      const arg = this.parseUnary();
      return this.base<UnaryExpr>({ kind: 'Unary', op: op.value, arg }, op, this.prev());
    }
    return this.parseCall();
  }

  parseCall(): Expr {
    let expr = this.parsePrimary();
    while (this.at('punct', '(')) {
      const open = this.expect('punct', '(');
      const args: Expr[] = [];
      if (!this.at('punct', ')')) {
        args.push(this.parseExpr());
        while (this.match('punct', ',')) args.push(this.parseExpr());
      }
      this.expect('punct', ')');
      expr = this.base<CallExpr>({ kind: 'Call', callee: expr, args }, open, this.prev());
    }
    return expr;
  }

  parsePrimary(): Expr {
    const t = this.peek();
    if (this.match('num')) {
      return this.base<NumLit>({ kind: 'Num', value: Number(t.value) }, t, t);
    }
    if (this.match('str')) {
      return this.base<StrLit>({ kind: 'Str', value: t.value }, t, t);
    }
    if (this.match('keyword', 'true')) {
      return this.base<BoolLit>({ kind: 'Bool', value: true }, t, t);
    }
    if (this.match('keyword', 'false')) {
      return this.base<BoolLit>({ kind: 'Bool', value: false }, t, t);
    }
    if (this.match('keyword', 'null')) {
      return this.base<NullLit>({ kind: 'Null' }, t, t);
    }
    if (this.match('ident')) {
      return this.base<Ident>({ kind: 'Ident', name: t.value }, t, t);
    }
    if (this.match('punct', '(')) {
      const expr = this.parseExpr();
      this.expect('punct', ')');
      return expr;
    }
    throw new ParseError(`unexpected '${t.value || t.type}' at line ${t.line}`);
  }
}

export function parse(source: string): Program {
  const parser = new Parser(source);
  const program = parser.parseProgram();
  parser.expect('eof');
  return program;
}

export function parseExpression(source: string): Expr {
  const parser = new Parser(source);
  const expr = parser.parseExpr();
  parser.expect('eof');
  return expr;
}
