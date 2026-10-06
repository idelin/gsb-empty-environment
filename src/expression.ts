/**
 * 有限布尔上下文表达式：标识符、true/false 字面量、!、&&、||、括号，
 * 以及 key == literal / key != literal 比较（右值仅限字符串/数字/布尔字面量）。
 * 语法是固定的有限集合，不支持函数调用或任意求值。
 */

export type Expr =
  | { kind: 'key'; name: string }
  | { kind: 'literal'; value: boolean }
  | { kind: 'not'; operand: Expr }
  | { kind: 'and'; left: Expr; right: Expr }
  | { kind: 'or'; left: Expr; right: Expr }
  | { kind: 'eq'; key: string; value: string | number | boolean; negate: boolean };

export class ConditionParseError extends Error {
  readonly offset: number;
  readonly source: string;

  constructor(message: string, offset: number, source: string) {
    super(`${message} (offset ${offset} in ${JSON.stringify(source)})`);
    this.name = 'ConditionParseError';
    this.offset = offset;
    this.source = source;
  }
}

type Token =
  | { type: 'ident'; value: string; offset: number }
  | { type: 'string'; value: string; offset: number }
  | { type: 'number'; value: number; offset: number }
  | { type: 'op'; value: '!' | '&&' | '||' | '(' | ')' | '==' | '!='; offset: number };

const IDENT_START = /[A-Za-z_]/;
const IDENT_PART = /[A-Za-z0-9_.-]/;
const DIGIT = /[0-9]/;

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < source.length) {
    const ch = source[i];
    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i++;
      continue;
    }
    if (IDENT_START.test(ch)) {
      let j = i + 1;
      while (j < source.length && IDENT_PART.test(source[j])) j++;
      tokens.push({ type: 'ident', value: source.slice(i, j), offset: i });
      i = j;
      continue;
    }
    if (DIGIT.test(ch) || (ch === '-' && DIGIT.test(source[i + 1] ?? ''))) {
      let j = ch === '-' ? i + 1 : i;
      while (j < source.length && DIGIT.test(source[j])) j++;
      if (source[j] === '.') {
        j++;
        while (j < source.length && DIGIT.test(source[j])) j++;
      }
      tokens.push({ type: 'number', value: Number(source.slice(i, j)), offset: i });
      i = j;
      continue;
    }
    if (ch === "'" || ch === '"') {
      const quote = ch;
      let j = i + 1;
      let value = '';
      for (;;) {
        if (j >= source.length) {
          throw new ConditionParseError('unterminated string literal', i, source);
        }
        const c = source[j];
        if (c === '\\') {
          const next = source[j + 1];
          if (next === quote || next === '\\') {
            value += next;
            j += 2;
            continue;
          }
        }
        if (c === quote) break;
        value += c;
        j++;
      }
      tokens.push({ type: 'string', value, offset: i });
      i = j + 1;
      continue;
    }
    const two = source.slice(i, i + 2);
    if (two === '&&' || two === '||' || two === '==' || two === '!=') {
      tokens.push({ type: 'op', value: two, offset: i });
      i += 2;
      continue;
    }
    if (ch === '!' || ch === '(' || ch === ')') {
      tokens.push({ type: 'op', value: ch, offset: i });
      i++;
      continue;
    }
    if (ch === '=' || ch === '&' || ch === '|') {
      throw new ConditionParseError(
        `unexpected '${ch}', did you mean '${ch}${ch === '=' ? '=' : ch}'?`,
        i,
        source,
      );
    }
    throw new ConditionParseError(`unexpected character '${ch}'`, i, source);
  }
  return tokens;
}

class Parser {
  private pos = 0;
  private readonly tokens: Token[];
  private readonly source: string;

  constructor(tokens: Token[], source: string) {
    this.tokens = tokens;
    this.source = source;
  }

  private peek(): Token | undefined {
    return this.tokens[this.pos];
  }

  private next(): Token {
    const token = this.tokens[this.pos];
    if (!token) {
      throw new ConditionParseError('unexpected end of expression', this.source.length, this.source);
    }
    this.pos++;
    return token;
  }

  private expectOp(value: string): void {
    const token = this.peek();
    if (!token || token.type !== 'op' || token.value !== value) {
      const offset = token ? token.offset : this.source.length;
      throw new ConditionParseError(`expected '${value}'`, offset, this.source);
    }
    this.pos++;
  }

  parseExpression(): Expr {
    const expr = this.parseOr();
    const rest = this.peek();
    if (rest) {
      throw new ConditionParseError('unexpected trailing token', rest.offset, this.source);
    }
    return expr;
  }

  private parseOr(): Expr {
    let left = this.parseAnd();
    for (;;) {
      const token = this.peek();
      if (token && token.type === 'op' && token.value === '||') {
        this.pos++;
        left = { kind: 'or', left, right: this.parseAnd() };
      } else {
        return left;
      }
    }
  }

  private parseAnd(): Expr {
    let left = this.parseEquality();
    for (;;) {
      const token = this.peek();
      if (token && token.type === 'op' && token.value === '&&') {
        this.pos++;
        left = { kind: 'and', left, right: this.parseEquality() };
      } else {
        return left;
      }
    }
  }

  private parseEquality(): Expr {
    const left = this.parseUnary();
    const token = this.peek();
    if (token && token.type === 'op' && (token.value === '==' || token.value === '!=')) {
      this.pos++;
      if (left.kind !== 'key') {
        throw new ConditionParseError(
          'left side of comparison must be a context key',
          token.offset,
          this.source,
        );
      }
      const rhs = this.next();
      let value: string | number | boolean;
      if (rhs.type === 'string') {
        value = rhs.value;
      } else if (rhs.type === 'number') {
        value = rhs.value;
      } else if (rhs.type === 'ident' && (rhs.value === 'true' || rhs.value === 'false')) {
        value = rhs.value === 'true';
      } else {
        throw new ConditionParseError(
          'right side of comparison must be a string, number, or boolean literal',
          rhs.offset,
          this.source,
        );
      }
      return { kind: 'eq', key: left.name, value, negate: token.value === '!=' };
    }
    return left;
  }

  private parseUnary(): Expr {
    const token = this.peek();
    if (token && token.type === 'op' && token.value === '!') {
      this.pos++;
      return { kind: 'not', operand: this.parseUnary() };
    }
    return this.parsePrimary();
  }

  private parsePrimary(): Expr {
    const token = this.next();
    if (token.type === 'op' && token.value === '(') {
      const expr = this.parseOr();
      this.expectOp(')');
      return expr;
    }
    if (token.type === 'ident') {
      if (token.value === 'true') return { kind: 'literal', value: true };
      if (token.value === 'false') return { kind: 'literal', value: false };
      return { kind: 'key', name: token.value };
    }
    throw new ConditionParseError('expected a context key, literal, or (', token.offset, this.source);
  }
}

export function parseCondition(source: string): Expr {
  const tokens = tokenize(source);
  if (tokens.length === 0) {
    throw new ConditionParseError('empty expression', 0, source);
  }
  return new Parser(tokens, source).parseExpression();
}

export function evaluateCondition(expr: Expr, get: (key: string) => unknown): boolean {
  switch (expr.kind) {
    case 'key':
      return Boolean(get(expr.name));
    case 'literal':
      return expr.value;
    case 'not':
      return !evaluateCondition(expr.operand, get);
    case 'and':
      return evaluateCondition(expr.left, get) && evaluateCondition(expr.right, get);
    case 'or':
      return evaluateCondition(expr.left, get) || evaluateCondition(expr.right, get);
    case 'eq': {
      const actual = get(expr.key);
      const equal = actual === expr.value;
      return expr.negate ? !equal : equal;
    }
  }
}
