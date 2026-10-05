/**
 * Boolean condition expressions for `// #if` / `// #elif` directives.
 *
 * Grammar (precedence low -> high):
 *   or      := and ( '||' and )*
 *   and     := unary ( '&&' unary )*
 *   unary   := '!' unary | primary
 *   primary := '(' or ')' | operand ( ('=='|'!=') operand )?
 *   operand := identifier | string | number | 'true' | 'false'
 *
 * Evaluation is lazy: `&&` and `||` short-circuit, so an unknown variable
 * on a branch that is never evaluated does not raise an error.
 */

import { ExpressionError, UnknownVariableError } from './errors.ts';

export type VarValue = boolean | string | number;
export type VarTable = Record<string, VarValue>;

export type Operand =
  | { kind: 'ident'; name: string }
  | { kind: 'literal'; value: VarValue };

export type Expr =
  | { kind: 'or'; left: Expr; right: Expr }
  | { kind: 'and'; left: Expr; right: Expr }
  | { kind: 'not'; operand: Expr }
  | { kind: 'eq'; left: Operand; right: Operand; negated: boolean }
  | { kind: 'bool'; value: boolean }
  | { kind: 'ident'; name: string };

type Token =
  | { kind: 'lparen' }
  | { kind: 'rparen' }
  | { kind: 'not' }
  | { kind: 'and' }
  | { kind: 'or' }
  | { kind: 'eqeq' }
  | { kind: 'neq' }
  | { kind: 'ident'; name: string }
  | { kind: 'literal'; value: VarValue };

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

function isIdentStart(ch: string): boolean {
  return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch === '_';
}

function isIdentPart(ch: string): boolean {
  return isIdentStart(ch) || isDigit(ch);
}

function tokenize(input: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < input.length) {
    const ch = input[i];
    if (ch === ' ' || ch === '\t') {
      i++;
    } else if (ch === '(') {
      tokens.push({ kind: 'lparen' });
      i++;
    } else if (ch === ')') {
      tokens.push({ kind: 'rparen' });
      i++;
    } else if (ch === '!') {
      if (input[i + 1] === '=') {
        tokens.push({ kind: 'neq' });
        i += 2;
      } else {
        tokens.push({ kind: 'not' });
        i++;
      }
    } else if (ch === '&') {
      if (input[i + 1] !== '&') throw new ExpressionError("unexpected '&'; did you mean '&&'?");
      tokens.push({ kind: 'and' });
      i += 2;
    } else if (ch === '|') {
      if (input[i + 1] !== '|') throw new ExpressionError("unexpected '|'; did you mean '||'?");
      tokens.push({ kind: 'or' });
      i += 2;
    } else if (ch === '=') {
      if (input[i + 1] !== '=') throw new ExpressionError("unexpected '='; did you mean '=='?");
      tokens.push({ kind: 'eqeq' });
      i += 2;
    } else if (ch === '"' || ch === "'") {
      let j = i + 1;
      let value = '';
      while (j < input.length && input[j] !== ch) {
        if (input[j] === '\\' && j + 1 < input.length) {
          value += input[j + 1];
          j += 2;
        } else {
          value += input[j];
          j++;
        }
      }
      if (j >= input.length) throw new ExpressionError('unterminated string literal');
      tokens.push({ kind: 'literal', value });
      i = j + 1;
    } else if (isDigit(ch)) {
      let j = i;
      while (j < input.length && (isDigit(input[j]) || input[j] === '.')) j++;
      const raw = input.slice(i, j);
      const value = Number(raw);
      if (Number.isNaN(value)) throw new ExpressionError(`invalid number '${raw}'`);
      tokens.push({ kind: 'literal', value });
      i = j;
    } else if (isIdentStart(ch)) {
      let j = i;
      while (j < input.length && isIdentPart(input[j])) j++;
      const name = input.slice(i, j);
      if (name === 'true') tokens.push({ kind: 'literal', value: true });
      else if (name === 'false') tokens.push({ kind: 'literal', value: false });
      else tokens.push({ kind: 'ident', name });
      i = j;
    } else {
      throw new ExpressionError(`unexpected character '${ch}'`);
    }
  }
  return tokens;
}

class Parser {
  private pos = 0;

  private readonly tokens: Token[];

  constructor(tokens: Token[]) {
    this.tokens = tokens;
  }

  private peek(): Token | undefined {
    return this.tokens[this.pos];
  }

  private next(): Token {
    const tok = this.tokens[this.pos];
    if (tok === undefined) throw new ExpressionError('unexpected end of expression');
    this.pos++;
    return tok;
  }

  atEnd(): boolean {
    return this.pos >= this.tokens.length;
  }

  parseOr(): Expr {
    let left = this.parseAnd();
    while (this.peek()?.kind === 'or') {
      this.next();
      left = { kind: 'or', left, right: this.parseAnd() };
    }
    return left;
  }

  private parseAnd(): Expr {
    let left = this.parseUnary();
    while (this.peek()?.kind === 'and') {
      this.next();
      left = { kind: 'and', left, right: this.parseUnary() };
    }
    return left;
  }

  private parseUnary(): Expr {
    if (this.peek()?.kind === 'not') {
      this.next();
      return { kind: 'not', operand: this.parseUnary() };
    }
    return this.parsePrimary();
  }

  private parsePrimary(): Expr {
    const tok = this.peek();
    if (tok === undefined) throw new ExpressionError('unexpected end of expression');
    if (tok.kind === 'lparen') {
      this.next();
      const inner = this.parseOr();
      const closing = this.next();
      if (closing.kind !== 'rparen') throw new ExpressionError("expected ')'");
      return inner;
    }
    if (tok.kind === 'ident') {
      this.next();
      return this.parseComparisonRest({ kind: 'ident', name: tok.name });
    }
    if (tok.kind === 'literal') {
      this.next();
      return this.parseComparisonRest({ kind: 'literal', value: tok.value });
    }
    throw new ExpressionError('expected identifier, literal or (');
  }

  private parseComparisonRest(left: Operand): Expr {
    const tok = this.peek();
    if (tok !== undefined && (tok.kind === 'eqeq' || tok.kind === 'neq')) {
      this.next();
      return { kind: 'eq', left, right: this.parseOperand(), negated: tok.kind === 'neq' };
    }
    if (left.kind === 'ident') return { kind: 'ident', name: left.name };
    if (typeof left.value === 'boolean') return { kind: 'bool', value: left.value };
    throw new ExpressionError('a string or number literal is only allowed in a comparison');
  }

  private parseOperand(): Operand {
    const tok = this.next();
    if (tok.kind === 'ident') return { kind: 'ident', name: tok.name };
    if (tok.kind === 'literal') return { kind: 'literal', value: tok.value };
    throw new ExpressionError('expected identifier or literal after comparison operator');
  }
}

export function parseExpression(input: string): Expr {
  const parser = new Parser(tokenize(input));
  const expr = parser.parseOr();
  if (!parser.atEnd()) throw new ExpressionError('unexpected trailing tokens');
  return expr;
}

function lookup(name: string, vars: VarTable): VarValue {
  if (!Object.prototype.hasOwnProperty.call(vars, name)) {
    throw new UnknownVariableError(name);
  }
  const value = vars[name];
  if (typeof value !== 'boolean' && typeof value !== 'string' && typeof value !== 'number') {
    throw new ExpressionError(`variable '${name}' must be a boolean, string or number`);
  }
  return value;
}

function resolveOperand(operand: Operand, vars: VarTable): VarValue {
  return operand.kind === 'literal' ? operand.value : lookup(operand.name, vars);
}

export function evaluate(expr: Expr, vars: VarTable): boolean {
  switch (expr.kind) {
    case 'bool':
      return expr.value;
    case 'ident': {
      const value = lookup(expr.name, vars);
      if (typeof value !== 'boolean') {
        throw new ExpressionError(`variable '${expr.name}' is used as a condition but is not a boolean`);
      }
      return value;
    }
    case 'not':
      return !evaluate(expr.operand, vars);
    case 'and':
      return evaluate(expr.left, vars) && evaluate(expr.right, vars);
    case 'or':
      return evaluate(expr.left, vars) || evaluate(expr.right, vars);
    case 'eq': {
      const equal = resolveOperand(expr.left, vars) === resolveOperand(expr.right, vars);
      return expr.negated ? !equal : equal;
    }
  }
}
