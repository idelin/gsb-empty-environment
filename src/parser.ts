import { lex } from './lexer.ts';
import type { Token } from './lexer.ts';
import type { BinaryExpr, Expr, Program, Stmt } from './ast.ts';

export class ParseError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(message);
    this.name = 'ParseError';
    this.offset = offset;
  }
}

class Parser {
  private pos = 0;
  private readonly tokens: Token[];
  constructor(tokens: Token[]) {
    this.tokens = tokens;
  }

  private peek(): Token {
    return this.tokens[this.pos];
  }

  private next(): Token {
    return this.tokens[this.pos++];
  }

  private expect(kind: Token['kind'], what: string): Token {
    const tok = this.peek();
    if (tok.kind !== kind) {
      throw new ParseError(`expected ${what}, found ${tok.text || tok.kind}`, tok.start);
    }
    return this.next();
  }

  parseProgram(): Program {
    const body: Stmt[] = [];
    while (this.peek().kind !== 'eof') body.push(this.parseStmt());
    const eof = this.peek();
    return { body, span: { start: 0, end: eof.end } };
  }

  private parseStmt(): Stmt {
    const tok = this.peek();
    if (tok.kind === 'let') return this.parseLet();
    if (tok.kind === 'lbrace') return this.parseBlock();
    const expr = this.parseExpr();
    const semi = this.expect('semi', "';' after expression statement");
    return { kind: 'expr', expr, span: { start: expr.span.start, end: semi.end } };
  }

  private parseLet(): Stmt {
    const kw = this.expect('let', "'let'");
    const name = this.expect('ident', 'identifier after let');
    this.expect('eq', "'=' in let statement");
    const init = this.parseExpr();
    const semi = this.expect('semi', "';' after let statement");
    return {
      kind: 'let',
      name: name.text,
      nameSpan: { start: name.start, end: name.end },
      init,
      span: { start: kw.start, end: semi.end },
    };
  }

  private parseBlock(): Stmt {
    const open = this.expect('lbrace', "'{'");
    const body: Stmt[] = [];
    while (this.peek().kind !== 'rbrace') {
      if (this.peek().kind === 'eof') {
        throw new ParseError('unterminated block', open.start);
      }
      body.push(this.parseStmt());
    }
    const close = this.next();
    return { kind: 'block', body, span: { start: open.start, end: close.end } };
  }

  private parseExpr(): Expr {
    let left = this.parsePrimary();
    for (;;) {
      const tok = this.peek();
      if (tok.kind !== 'plus' && tok.kind !== 'star') break;
      this.next();
      const right = this.parsePrimary();
      const node: BinaryExpr = {
        kind: 'binary',
        op: tok.kind === 'plus' ? '+' : '*',
        left,
        right,
        span: { start: left.span.start, end: right.span.end },
      };
      left = node;
    }
    return left;
  }

  private parsePrimary(): Expr {
    const tok = this.peek();
    if (tok.kind === 'ident') {
      this.next();
      return { kind: 'ident', name: tok.text, span: { start: tok.start, end: tok.end } };
    }
    if (tok.kind === 'number') {
      this.next();
      return { kind: 'num', value: Number(tok.text), span: { start: tok.start, end: tok.end } };
    }
    if (tok.kind === 'lparen') {
      this.next();
      const inner = this.parseExpr();
      this.expect('rparen', "')'");
      return inner;
    }
    throw new ParseError(`expected expression, found ${tok.text || tok.kind}`, tok.start);
  }
}

export function parse(source: string): Program {
  return new Parser(lex(source)).parseProgram();
}
