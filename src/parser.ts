import type { FilterExpr, Literal, Operand, PathAst, Segment } from './ast.ts';
import { ParseError } from './errors.ts';
import { tokenize, type Token } from './lexer.ts';
import { DENIED_KEYS } from './safeAccess.ts';

export interface ParseLimits {
  /** Max number of path segments (depth). Exceeding fails at parse time. */
  maxDepth: number;
  /** Max number of recursive-descent (`..`) segments. */
  maxDescents: number;
  /** Max literals in an `in [...]` set. */
  maxSetSize: number;
  /** Max expression length in characters. */
  maxLength: number;
}

export const DEFAULT_LIMITS: ParseLimits = {
  maxDepth: 32,
  maxDescents: 4,
  maxSetSize: 64,
  maxLength: 512,
};

class Parser {
  private pos = 0;
  private depth = 0;
  private descents = 0;
  private readonly src: string;
  private readonly tokens: Token[];
  private readonly limits: ParseLimits;

  constructor(src: string, tokens: Token[], limits: ParseLimits) {
    this.src = src;
    this.tokens = tokens;
    this.limits = limits;
  }

  private peek(): Token { return this.tokens[this.pos]; }
  private next(): Token { return this.tokens[this.pos++]; }

  private fail(message: string): never {
    throw new ParseError(message, this.src, this.pos);
  }

  private expect(type: Token['type'], what: string): Token {
    const t = this.peek();
    if (t.type !== type) this.fail(`expected ${what}, found ${describe(t)}`);
    return this.next();
  }

  private checkKey(key: string): void {
    if (DENIED_KEYS.has(key)) {
      this.fail(`access to property ${JSON.stringify(key)} is denied by policy`);
    }
  }

  private addDepth(): void {
    this.depth += 1;
    if (this.depth > this.limits.maxDepth) {
      this.fail(`path too deep: exceeds limit of ${this.limits.maxDepth} segments`);
    }
  }

  parsePath(): PathAst {
    if (this.peek().type === 'dollar') this.next();
    const segments: Segment[] = [];
    while (this.peek().type !== 'eof') {
      const t = this.peek();
      if (t.type === 'dot') {
        this.next();
        segments.push(this.parseChild());
      } else if (t.type === 'dotdot') {
        this.next();
        this.descents += 1;
        if (this.descents > this.limits.maxDescents) {
          this.fail(`too many recursive descents: limit is ${this.limits.maxDescents}`);
        }
        this.addDepth();
        const key = this.parseName('descent target');
        this.checkKey(key);
        segments.push({ type: 'descent', key });
      } else if (t.type === 'lbracket') {
        segments.push(this.parseBracket());
      } else {
        this.fail(`expected '.', '..' or '[', found ${describe(t)}`);
      }
    }
    return { segments };
  }

  private parseName(what: string): string {
    const t = this.next();
    if (t.type === 'ident') return t.value;
    if (t.type === 'string') return t.value;
    this.fail(`expected ${what} name, found ${describe(t)}`);
  }

  private parseChild(): Segment {
    this.addDepth();
    const key = this.parseName('property');
    this.checkKey(key);
    return { type: 'child', key };
  }

  private parseBracket(): Segment {
    this.expect('lbracket', "'['");
    this.addDepth();
    const t = this.peek();
    if (t.type === 'int') {
      this.next();
      this.expect('rbracket', "']'");
      return { type: 'index', index: t.value };
    }
    if (t.type === 'star') {
      this.next();
      this.expect('rbracket', "']'");
      return { type: 'wildcard' };
    }
    if (t.type === 'question') {
      this.next();
      this.expect('lparen', "'(' after '?'");
      const expr = this.parseOr();
      this.expect('rparen', "')'");
      this.expect('rbracket', "']'");
      return { type: 'filter', expr };
    }
    this.fail(`expected integer, '*' or '?(' inside brackets, found ${describe(t)}`);
  }

  private parseOr(): FilterExpr {
    const items: FilterExpr[] = [this.parseAnd()];
    while (this.peek().type === 'or') {
      this.next();
      items.push(this.parseAnd());
    }
    return items.length === 1 ? items[0] : { type: 'or', items };
  }

  private parseAnd(): FilterExpr {
    const items: FilterExpr[] = [this.parseUnary()];
    while (this.peek().type === 'and') {
      this.next();
      items.push(this.parseUnary());
    }
    return items.length === 1 ? items[0] : { type: 'and', items };
  }

  private parseUnary(): FilterExpr {
    const t = this.peek();
    if (t.type === 'bang') {
      this.next();
      return { type: 'not', expr: this.parseUnary() };
    }
    if (t.type === 'lparen') {
      this.next();
      const inner = this.parseOr();
      this.expect('rparen', "')'");
      return inner;
    }
    if (t.type === 'ident' && t.value === 'exists') {
      this.next();
      this.expect('lparen', "'(' after 'exists'");
      const path = this.parseAtPath();
      this.expect('rparen', "')'");
      return { type: 'exists', path };
    }
    return this.parseComparison();
  }

  private parseAtPath(): string[] {
    this.expect('at', "'@'");
    const path: string[] = [];
    while (this.peek().type === 'dot') {
      this.next();
      const key = this.parseName('field');
      this.checkKey(key);
      path.push(key);
    }
    return path;
  }

  private parseComparison(): FilterExpr {
    const left = this.parseOperand();
    const t = this.peek();
    if (t.type === 'op') {
      this.next();
      const right = this.parseOperand();
      return { type: 'compare', op: t.value, left, right };
    }
    if (t.type === 'ident' && t.value === 'in') {
      this.next();
      this.expect('lbracket', "'[' after 'in'");
      const set: Literal[] = [];
      if (this.peek().type !== 'rbracket') {
        set.push(this.parseLiteral());
        while (this.peek().type === 'comma') {
          this.next();
          set.push(this.parseLiteral());
        }
      }
      this.expect('rbracket', "']'");
      if (set.length === 0) this.fail("'in' requires a non-empty finite set");
      if (set.length > this.limits.maxSetSize) {
        this.fail(`'in' set too large: limit is ${this.limits.maxSetSize} literals`);
      }
      return { type: 'in', left, set };
    }
    if (left.type === 'literal') {
      this.fail('a bare literal is not a valid condition; compare it with @');
    }
    return { type: 'truthy', path: left.type === 'self' ? [] : left.path };
  }

  private parseOperand(): Operand {
    const t = this.peek();
    if (t.type === 'at') {
      const path = this.parseAtPath();
      return path.length === 0 ? { type: 'self' } : { type: 'field', path };
    }
    return { type: 'literal', value: this.parseLiteral() };
  }

  private parseLiteral(): Literal {
    const t = this.next();
    if (t.type === 'int') return t.value;
    if (t.type === 'string') return t.value;
    if (t.type === 'ident') {
      if (t.value === 'true') return true;
      if (t.value === 'false') return false;
      if (t.value === 'undefined') return undefined;
    }
    this.fail(`expected literal (integer, string, true, false, undefined), found ${describe(t)}`);
  }
}

function describe(t: Token): string {
  if (t.type === 'eof') return 'end of expression';
  if (t.type === 'ident') return `identifier ${JSON.stringify(t.value)}`;
  if (t.type === 'int') return `integer ${t.value}`;
  if (t.type === 'string') return `string ${JSON.stringify(t.value)}`;
  if (t.type === 'op') return `operator '${t.value}'`;
  return `'${t.type}'`;
}

/** Parse (and statically validate) a path expression into an AST. */
export function parse(expression: string, limits: Partial<ParseLimits> = {}): PathAst {
  const merged: ParseLimits = { ...DEFAULT_LIMITS, ...limits };
  if (typeof expression !== 'string' || expression.length === 0) {
    throw new ParseError('expression must be a non-empty string', String(expression));
  }
  if (expression.length > merged.maxLength) {
    throw new ParseError(
      `expression too long: ${expression.length} chars exceeds limit of ${merged.maxLength}`,
      expression,
    );
  }
  const parser = new Parser(expression, tokenize(expression), merged);
  return parser.parsePath();
}
