import { tokenize, type Token } from './lexer.ts';
import type { Expr, Program, Stmt } from './ast.ts';

export class ParseError extends Error {}

class Parser {
  private pos = 0;
  private nextId = 1;
  private readonly tokens: Token[];

  constructor(tokens: Token[]) {
    this.tokens = tokens;
  }

  private peek(): Token {
    return this.tokens[this.pos];
  }

  private take(): Token {
    return this.tokens[this.pos++];
  }

  private at(value: string): boolean {
    const t = this.peek();
    return t.type !== 'eof' && t.value === value;
  }

  private eat(value: string): boolean {
    if (this.at(value)) {
      this.pos++;
      return true;
    }
    return false;
  }

  private expect(value: string): Token {
    const t = this.peek();
    if (!this.at(value)) {
      throw new ParseError(`line ${t.line}: expected '${value}' but got '${t.value}'`);
    }
    return this.take();
  }

  private expectIdentifier(): Token {
    const t = this.peek();
    if (t.type !== 'identifier') {
      throw new ParseError(`line ${t.line}: expected identifier but got '${t.value}'`);
    }
    return this.take();
  }

  private node<T extends object>(line: number, fields: T): T & { id: number; line: number } {
    return { id: this.nextId++, line, ...fields };
  }

  parseProgram(): Program {
    const body: Stmt[] = [];
    while (this.peek().type !== 'eof') {
      body.push(this.parseStatement());
    }
    return { body };
  }

  private parseStatement(): Stmt {
    const t = this.peek();
    switch (t.value) {
      case 'let':
      case 'const': {
        const decl = this.parseVarDecl();
        this.expect(';');
        return decl;
      }
      case 'function':
        return this.parseFunctionDecl();
      case 'return': {
        this.take();
        const argument = this.at(';') ? null : this.parseExpr();
        this.expect(';');
        return this.node(t.line, { kind: 'Return', argument });
      }
      case 'if':
        return this.parseIf();
      case 'while':
        return this.parseWhile();
      case 'for':
        return this.parseFor();
      case '{':
        return this.parseBlock();
      case ';': {
        this.take();
        return this.parseStatement();
      }
      default: {
        const expr = this.parseExpr();
        this.expect(';');
        return this.node(t.line, { kind: 'ExprStmt', expr });
      }
    }
  }

  private parseVarDecl(): Stmt {
    const kw = this.take();
    const name = this.expectIdentifier();
    if (this.eat(':')) this.parseTypeAnnotation();
    const init = this.eat('=') ? this.parseExpr() : null;
    return this.node(kw.line, {
      kind: 'VarDecl',
      declKind: kw.value as 'let' | 'const',
      name: name.value,
      init,
    });
  }

  private parseTypeAnnotation(): void {
    this.expectIdentifier();
    while (this.eat('[')) this.expect(']');
  }

  private parseFunctionDecl(): Stmt {
    const kw = this.expect('function');
    const name = this.expectIdentifier();
    this.expect('(');
    const params: string[] = [];
    if (!this.at(')')) {
      do {
        params.push(this.expectIdentifier().value);
        if (this.eat(':')) this.parseTypeAnnotation();
      } while (this.eat(','));
    }
    this.expect(')');
    if (this.eat(':')) this.parseTypeAnnotation();
    this.expect('{');
    const body: Stmt[] = [];
    while (!this.at('}')) body.push(this.parseStatement());
    this.expect('}');
    return this.node(kw.line, { kind: 'FunctionDecl', name: name.value, params, body });
  }

  private parseBlock(): Stmt {
    const open = this.expect('{');
    const body: Stmt[] = [];
    while (!this.at('}')) body.push(this.parseStatement());
    this.expect('}');
    return this.node(open.line, { kind: 'Block', body });
  }

  private parseIf(): Stmt {
    const kw = this.expect('if');
    this.expect('(');
    const test = this.parseExpr();
    this.expect(')');
    const consequent = this.parseStatement();
    const alternate = this.eat('else') ? this.parseStatement() : null;
    return this.node(kw.line, { kind: 'If', test, consequent, alternate });
  }

  private parseWhile(): Stmt {
    const kw = this.expect('while');
    this.expect('(');
    const test = this.parseExpr();
    this.expect(')');
    const body = this.parseStatement();
    return this.node(kw.line, { kind: 'While', test, body });
  }

  private parseFor(): Stmt {
    const kw = this.expect('for');
    this.expect('(');
    let init: Stmt | null = null;
    if (!this.at(';')) {
      const t = this.peek();
      if (t.value === 'let' || t.value === 'const') {
        init = this.parseVarDecl();
      } else {
        init = this.node(t.line, { kind: 'ExprStmt', expr: this.parseExpr() });
      }
    }
    this.expect(';');
    const test = this.at(';') ? null : this.parseExpr();
    this.expect(';');
    const update = this.at(')') ? null : this.parseExpr();
    this.expect(')');
    const body = this.parseStatement();
    return this.node(kw.line, { kind: 'For', init, test, update, body });
  }

  parseExpr(): Expr {
    return this.parseAssign();
  }

  private parseAssign(): Expr {
    const left = this.parseOr();
    if (this.at('=')) {
      const op = this.take();
      if (left.kind !== 'Identifier') {
        throw new ParseError(`line ${op.line}: invalid assignment target`);
      }
      const value = this.parseAssign();
      return this.node(op.line, { kind: 'Assign', name: left.name, value });
    }
    return left;
  }

  private parseOr(): Expr {
    let left = this.parseAnd();
    while (this.at('||')) {
      const op = this.take();
      left = this.node(op.line, { kind: 'Binary', op: '||', left, right: this.parseAnd() });
    }
    return left;
  }

  private parseAnd(): Expr {
    let left = this.parseEquality();
    while (this.at('&&')) {
      const op = this.take();
      left = this.node(op.line, { kind: 'Binary', op: '&&', left, right: this.parseEquality() });
    }
    return left;
  }

  private parseEquality(): Expr {
    let left = this.parseComparison();
    while (this.at('==') || this.at('===') || this.at('!=') || this.at('!==')) {
      const op = this.take();
      left = this.node(op.line, { kind: 'Binary', op: op.value, left, right: this.parseComparison() });
    }
    return left;
  }

  private parseComparison(): Expr {
    let left = this.parseAdditive();
    while (this.at('<') || this.at('<=') || this.at('>') || this.at('>=')) {
      const op = this.take();
      left = this.node(op.line, { kind: 'Binary', op: op.value, left, right: this.parseAdditive() });
    }
    return left;
  }

  private parseAdditive(): Expr {
    let left = this.parseMultiplicative();
    while (this.at('+') || this.at('-')) {
      const op = this.take();
      left = this.node(op.line, { kind: 'Binary', op: op.value, left, right: this.parseMultiplicative() });
    }
    return left;
  }

  private parseMultiplicative(): Expr {
    let left = this.parseUnary();
    while (this.at('*') || this.at('/') || this.at('%')) {
      const op = this.take();
      left = this.node(op.line, { kind: 'Binary', op: op.value, left, right: this.parseUnary() });
    }
    return left;
  }

  private parseUnary(): Expr {
    if (this.at('!') || this.at('-')) {
      const op = this.take();
      return this.node(op.line, { kind: 'Unary', op: op.value, argument: this.parseUnary() });
    }
    return this.parseCall();
  }

  private parseCall(): Expr {
    let expr = this.parsePrimary();
    while (this.at('(')) {
      const open = this.take();
      const args: Expr[] = [];
      if (!this.at(')')) {
        do {
          args.push(this.parseExpr());
        } while (this.eat(','));
      }
      this.expect(')');
      expr = this.node(open.line, { kind: 'Call', callee: expr, args });
    }
    return expr;
  }

  private parsePrimary(): Expr {
    const t = this.peek();
    if (t.type === 'number') {
      this.take();
      return this.node(t.line, { kind: 'NumberLit', value: parseFloat(t.value) });
    }
    if (t.type === 'string') {
      this.take();
      return this.node(t.line, { kind: 'StringLit', value: t.value });
    }
    if (t.value === 'true' || t.value === 'false') {
      this.take();
      return this.node(t.line, { kind: 'BoolLit', value: t.value === 'true' });
    }
    if (t.type === 'identifier') {
      this.take();
      return this.node(t.line, { kind: 'Identifier', name: t.value });
    }
    if (this.eat('(')) {
      const expr = this.parseExpr();
      this.expect(')');
      return expr;
    }
    throw new ParseError(`line ${t.line}: unexpected token '${t.value}'`);
  }

  expectEnd(): void {
    const t = this.peek();
    if (t.type !== 'eof') {
      throw new ParseError(`line ${t.line}: unexpected trailing token '${t.value}'`);
    }
  }
}

export function parse(source: string): Program {
  const parser = new Parser(tokenize(source));
  const program = parser.parseProgram();
  parser.expectEnd();
  return program;
}

export function parseExpression(source: string): Expr {
  const parser = new Parser(tokenize(source));
  const expr = parser.parseExpr();
  parser.expectEnd();
  return expr;
}
