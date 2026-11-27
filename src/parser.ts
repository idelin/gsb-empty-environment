import type {
  BinaryExpr,
  BinaryOperator,
  Expr,
  JsonPathAst,
  LiteralExpr,
  PathMode,
  PathExpr,
  Step,
  Subscript,
  UnaryExpr,
  UnaryOperator,
} from './ast.ts';
import { JsonPathError } from './errors.ts';
import { tokenize, type Token } from './lexer.ts';

const METHODS = new Set(['type', 'size', 'double']);

export class Parser {
  private readonly tokens: Token[];
  private index = 0;

  constructor(input: string) {
    this.tokens = tokenize(input);
  }

  static parse(input: string): JsonPathAst {
    return new Parser(input).parsePathDocument();
  }

  private peek(offset = 0): Token {
    return this.tokens[Math.min(this.index + offset, this.tokens.length - 1)];
  }

  private next(): Token {
    const token = this.tokens[this.index];
    if (this.index < this.tokens.length - 1) this.index += 1;
    return token;
  }

  private isPunct(value: string): boolean {
    const token = this.peek();
    return token.type === 'punct' && token.value === value;
  }

  private isIdent(value: string): boolean {
    const token = this.peek();
    return token.type === 'ident' && token.value === value;
  }

  private expectPunct(value: string): Token {
    const token = this.peek();
    if (token.type !== 'punct' || token.value !== value) {
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        `expected ${JSON.stringify(value)} but found ${JSON.stringify(token.value || token.type)}`,
        token.pos,
      );
    }
    return this.next();
  }

  private parsePathDocument(): JsonPathAst {
    let mode: PathMode = 'lax';
    if (this.isIdent('lax') || this.isIdent('strict')) {
      mode = this.next().value as PathMode;
    }
    const expr = this.parsePath();
    if (this.peek().type !== 'eof') {
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        `unexpected trailing token ${JSON.stringify(this.peek().value)}`,
        this.peek().pos,
      );
    }
    return { mode, expr };
  }

  private parsePath(): PathExpr {
    const baseToken = this.peek();
    let base: JsonPathAst['expr']['base'];
    if (baseToken.type === 'root') {
      this.next();
      base = { type: 'root', pos: baseToken.pos };
    } else if (baseToken.type === 'current') {
      this.next();
      base = { type: 'current', pos: baseToken.pos };
    } else if (baseToken.type === 'variable') {
      this.next();
      base = { type: 'variable', name: baseToken.value, pos: baseToken.pos };
    } else {
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        `path must start with '$' or '@', found ${JSON.stringify(baseToken.value || baseToken.type)}`,
        baseToken.pos,
      );
    }

    const steps: Step[] = [];
    for (;;) {
      const step = this.tryParseStep();
      if (step === null) break;
      steps.push(step);
    }
    return { type: 'path', pos: baseToken.pos, base, steps };
  }

  private tryParseStep(): Step | null {
    const token = this.peek();
    if (token.type === 'punct' && token.value === '.') {
      const after = this.peek(1);
      if (after.type === 'punct' && (after.value === '.' || after.value === '**')) {
        this.next();
        this.next();
        return { type: 'recursiveDescendant', pos: token.pos };
      }
      if (after.type === 'punct' && (after.value === '*' || after.value === '**')) {
        this.next();
        this.next();
        return { type: 'wildcardMember', pos: token.pos };
      }
      if (after.type === 'ident') {
        this.next();
        const name = this.next();
        if (METHODS.has(name.value) && this.isPunct('(')) {
          this.next();
          this.expectPunct(')');
          return { type: 'method', pos: name.pos, name: name.value as 'type' | 'size' | 'double' };
        }
        return { type: 'member', pos: name.pos, key: name.value };
      }
      if (after.type === 'string') {
        this.next();
        const name = this.next();
        return { type: 'member', pos: name.pos, key: name.value };
      }
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        `expected member name after '.', found ${JSON.stringify(after.value || after.type)}`,
        after.pos,
      );
    }

    if (token.type === 'punct' && token.value === '[') {
      this.next();
      const subscripts: Subscript[] = [this.parseSubscript()];
      while (this.isPunct(',')) {
        this.next();
        subscripts.push(this.parseSubscript());
      }
      this.expectPunct(']');
      return { type: 'subscript', pos: token.pos, subscripts };
    }

    if (token.type === 'punct' && token.value === '?') {
      this.next();
      this.expectPunct('(');
      const predicate = this.parseExpression();
      this.expectPunct(')');
      return { type: 'filter', pos: token.pos, predicate };
    }

    return null;
  }

  private parseSubscript(): Subscript {
    const token = this.peek();
    if (this.isPunct('*')) {
      this.next();
      return { kind: 'wildcardIndex', pos: token.pos };
    }
    const start = this.tryParseSubscriptBound();
    if (this.isIdent('to')) {
      this.next();
      const end = this.parseSubscriptBound();
      return { kind: 'slice', pos: token.pos, start, end };
    }
    if (start === null) {
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        `invalid array subscript near ${JSON.stringify(token.value || token.type)}`,
        token.pos,
      );
    }
    return { kind: 'index', pos: token.pos, value: start };
  }

  /**
   * Negative/positive numeric literals are subscript signs, not binary
   * subtraction: `[-1]` is a literal index; `[last - 1]` stays an expression.
   */
  private tryParseSignedSubscript(): Expr | null {
    const token = this.peek();
    if (token.type === 'number') {
      this.next();
      return { type: 'literal', pos: token.pos, value: Number(token.value) };
    }
    if (token.type === 'punct' && (token.value === '-' || token.value === '+')) {
      const numberToken = this.peek(1);
      if (numberToken.type !== 'number') return null;
      this.next();
      this.next();
      const value = Number(numberToken.value);
      return {
        type: 'literal',
        pos: token.pos,
        value: token.value === '-' ? -value : value,
      };
    }
    return null;
  }

  private tryParseSubscriptBound(): Expr | null {
    if (this.isPunct('*') || this.isPunct(']') || this.isPunct(',')) return null;
    const signed = this.tryParseSignedSubscript();
    if (signed !== null) return signed;
    return this.parseAdditive();
  }

  private parseSubscriptBound(): Expr {
    const bound = this.tryParseSubscriptBound();
    if (bound === null) {
      throw new JsonPathError('JSON_PATH_SYNTAX', 'expected subscript bound after "to"', this.peek().pos);
    }
    return bound;
  }

  parseExpression(): Expr {
    return this.parseOr();
  }

  private parseBinaryLevel(
    operators: readonly BinaryOperator[],
    parseOperand: () => Expr,
  ): Expr {
    let left = parseOperand();
    while (this.peek().type === 'punct' && operators.includes(this.peek().value as BinaryOperator)) {
      const operator = this.next().value as BinaryOperator;
      const pos = this.tokens[this.index - 1].pos;
      const right = parseOperand();
      const node: BinaryExpr = { type: 'binary', pos, operator, left, right };
      left = node;
    }
    return left;
  }

  private parseOr(): Expr {
    return this.parseBinaryLevel(['||'], () => this.parseAnd());
  }

  private parseAnd(): Expr {
    return this.parseBinaryLevel(['&&'], () => this.parseNot());
  }

  private parseNot(): Expr {
    if (this.isPunct('!')) {
      const token = this.next();
      const argument = this.parseNot();
      const node: UnaryExpr = { type: 'unary', pos: token.pos, operator: '!', argument };
      return node;
    }
    return this.parseComparison();
  }

  private parseComparison(): Expr {
    const left = this.parseAdditive();
    const token = this.peek();
    const operators: BinaryOperator[] = ['==', '!=', '<=', '>=', '<', '>'];
    if (token.type === 'punct' && operators.includes(token.value as BinaryOperator)) {
      this.next();
      const right = this.parseAdditive();
      const likeRegex = this.tryParseLikeRegexTail(right);
      if (likeRegex !== null) return likeRegex;
      return { type: 'binary', pos: token.pos, operator: token.value as BinaryOperator, left, right };
    }
    const likeRegex = this.tryParseLikeRegexTail(left);
    if (likeRegex !== null) return likeRegex;
    return left;
  }

  private tryParseLikeRegexTail(left: Expr): Expr | null {
    if (!this.isIdent('like_regex')) return null;
    const keyword = this.next();
    const patternToken = this.peek();
    if (patternToken.type !== 'string') {
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        'like_regex requires a string pattern argument',
        patternToken.pos,
      );
    }
    this.next();
    let flags = '';
    if (this.isIdent('flag')) {
      this.next();
      const flagToken = this.peek();
      if (flagToken.type !== 'string' || flagToken.value.length === 0) {
        throw new JsonPathError(
          'JSON_PATH_SYNTAX',
          'flag argument must be a non-empty string literal',
          flagToken.pos,
        );
      }
      this.next();
      flags = flagToken.value;
    }
    return {
      type: 'likeRegex',
      pos: keyword.pos,
      left,
      pattern: patternToken.value,
      flags,
    };
  }

  private parseAdditive(): Expr {
    return this.parseBinaryLevel(['+', '-'], () => this.parseMultiplicative());
  }

  private parseMultiplicative(): Expr {
    return this.parseBinaryLevel(['*', '/', '%'], () => this.parseUnary());
  }

  private parseUnary(): Expr {
    const token = this.peek();
    if (token.type === 'punct' && (token.value === '+' || token.value === '-')) {
      this.next();
      const argument = this.parseUnary();
      const node: UnaryExpr = { type: 'unary', pos: token.pos, operator: token.value as UnaryOperator, argument };
      return node;
    }
    return this.parsePrimary();
  }

  private parsePrimary(): Expr {
    const token = this.peek();

    if (token.type === 'number') {
      this.next();
      const value = Number(token.value);
      const literal: LiteralExpr = { type: 'literal', pos: token.pos, value };
      return literal;
    }
    if (token.type === 'string') {
      this.next();
      return { type: 'literal', pos: token.pos, value: token.value };
    }
    if (token.type === 'ident') {
      if (token.value === 'true' || token.value === 'false') {
        this.next();
        return { type: 'literal', pos: token.pos, value: token.value === 'true' };
      }
      if (token.value === 'null') {
        this.next();
        return { type: 'literal', pos: token.pos, value: null };
      }
      if (token.value === 'last') {
        this.next();
        return { type: 'variable', name: 'last', pos: token.pos };
      }
      if (token.value === 'exists') {
        this.next();
        this.expectPunct('(');
        const argument = this.parsePath();
        this.expectPunct(')');
        return { type: 'exists', pos: token.pos, argument: argument as Extract<Expr, { type: 'path' }> };
      }
      throw new JsonPathError(
        'JSON_PATH_SYNTAX',
        `unexpected keyword ${JSON.stringify(token.value)}`,
        token.pos,
      );
    }
    if (token.type === 'punct' && token.value === '(') {
      this.next();
      const inner = this.parseExpression();
      this.expectPunct(')');
      return inner;
    }
    if (token.type === 'root' || token.type === 'current' || token.type === 'variable') {
      return this.parsePath();
    }
    throw new JsonPathError(
      'JSON_PATH_SYNTAX',
      `unexpected token ${JSON.stringify(token.value || token.type)}`,
      token.pos,
    );
  }
}

export function parseJsonPath(input: string): JsonPathAst {
  return Parser.parse(input);
}
