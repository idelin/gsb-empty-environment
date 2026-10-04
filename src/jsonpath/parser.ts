import type { Expr, PathSegment, Query, CmpOp } from './ast.ts';
import { ParseError } from './errors.ts';
import type { BudgetTracker } from './budget.ts';

/**
 * 查询语法（JSONPath 子集）：
 *   query    := '$' segment*
 *   segment  := '..' (name | '*' | bracket) | '.' (name | '*') | bracket
 *   bracket  := '[' (quoted | int | slice | '*' | filter) ']'
 *   slice    := [int] ':' [int] [':' [int]]
 *   filter   := '?' '(' or ')'
 *   or       := and ('||' and)*
 *   and      := unary ('&&' unary)*
 *   unary    := '!' unary | '(' or ')' | comparison
 *   comparison := operand (cmpOp operand)?   // 无运算符即存在性判断
 *   operand  := literal | '@' relPath | '$' relPath
 * 含斜杠等特殊字符的键必须使用括号记法：$['a/b']
 */
export function parse(source: string, budget?: BudgetTracker): Query {
  const p = new Parser(source, budget);
  const segments = p.parseQuery();
  return { source, segments };
}

const NAME_CHAR = /[A-Za-z0-9_$\-‑-￮]/u;

class Parser {
  pos = 0;
  private readonly src: string;
  private readonly budget?: BudgetTracker;
  constructor(src: string, budget?: BudgetTracker) {
    this.src = src;
    this.budget = budget;
  }

  private error(msg: string, pos = this.pos): never {
    throw new ParseError(msg, pos, this.src);
  }

  private tick(): void {
    this.budget?.consume('parse');
  }

  private peek(offset = 0): string {
    return this.src[this.pos + offset] ?? '';
  }

  private skipWs(): void {
    while (this.peek() === ' ' || this.peek() === '\t' || this.peek() === '\n') this.pos++;
  }

  parseQuery(): PathSegment[] {
    this.tick();
    if (this.peek() !== '$') this.error(`query must start with '$'`, 0);
    this.pos++;
    const segments: PathSegment[] = [];
    while (this.pos < this.src.length) {
      this.tick();
      const c = this.peek();
      if (c === '.') {
        if (this.peek(1) === '.') {
          this.pos += 2;
          segments.push({ kind: 'recursive' });
          const t = this.peek();
          if (t === '*') {
            this.pos++;
            segments.push({ kind: 'wildcard' });
          } else if (t === '[') {
            this.parseBracketInto(segments);
          } else {
            segments.push({ kind: 'child', key: this.parseName() });
          }
        } else {
          this.pos++;
          if (this.peek() === '*') {
            this.pos++;
            segments.push({ kind: 'wildcard' });
          } else {
            segments.push({ kind: 'child', key: this.parseName() });
          }
        }
      } else if (c === '[') {
        this.parseBracketInto(segments);
      } else {
        this.error(`unexpected character '${c}'`);
      }
    }
    return segments;
  }

  private parseName(): string {
    const start = this.pos;
    while (this.pos < this.src.length && NAME_CHAR.test(this.peek())) this.pos++;
    if (this.pos === start) this.error(`expected a field name`, start);
    return this.src.slice(start, this.pos);
  }

  private parseBracketInto(segments: PathSegment[]): void {
    this.pos++; // [
    this.skipWs();
    const c = this.peek();
    if (c === "'" || c === '"') {
      const key = this.parseQuoted();
      this.skipWs();
      this.expect(']');
      segments.push({ kind: 'child', key });
      return;
    }
    if (c === '*') {
      this.pos++;
      this.skipWs();
      this.expect(']');
      segments.push({ kind: 'wildcard' });
      return;
    }
    if (c === '?') {
      this.pos++;
      this.skipWs();
      this.expect('(');
      const expr = this.parseOr();
      this.skipWs();
      this.expect(')');
      this.skipWs();
      this.expect(']');
      segments.push({ kind: 'filter', expr });
      return;
    }
    // 数字或切片
    const first = this.tryParseInt();
    this.skipWs();
    if (this.peek() === ':') {
      this.pos++;
      this.skipWs();
      const second = this.tryParseInt();
      this.skipWs();
      let step: number | null = null;
      if (this.peek() === ':') {
        this.pos++;
        this.skipWs();
        step = this.tryParseInt();
        this.skipWs();
      }
      if (step === 0) this.error('slice step must not be 0');
      this.expect(']');
      segments.push({ kind: 'slice', start: first, end: second, step: step ?? 1 });
      return;
    }
    if (first === null) this.error(`expected string, number, slice, '*' or filter inside brackets`);
    this.expect(']');
    segments.push({ kind: 'index', index: first });
  }

  private tryParseInt(): number | null {
    const m = /^-?\d+/.exec(this.src.slice(this.pos));
    if (!m) return null;
    this.pos += m[0].length;
    return Number(m[0]);
  }

  private parseQuoted(): string {
    const quote = this.peek();
    this.pos++;
    let out = '';
    while (this.pos < this.src.length) {
      const c = this.peek();
      this.pos++;
      if (c === quote) return out;
      if (c === '\\') {
        const e = this.peek();
        this.pos++;
        switch (e) {
          case 'n': out += '\n'; break;
          case 't': out += '\t'; break;
          case 'r': out += '\r'; break;
          case 'u': {
            const hex = this.src.slice(this.pos, this.pos + 4);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) this.error('bad \\u escape');
            out += String.fromCharCode(parseInt(hex, 16));
            this.pos += 4;
            break;
          }
          default: out += e; // \\ \' \" \/ 等
        }
      } else {
        out += c;
      }
    }
    this.error('unterminated string literal');
  }

  private expect(ch: string): void {
    if (this.peek() !== ch) this.error(`expected '${ch}'`);
    this.pos++;
  }

  // ---- 谓词表达式 ----

  private parseOr(): Expr {
    let left = this.parseAnd();
    for (;;) {
      this.skipWs();
      if (this.peek() === '|' && this.peek(1) === '|') {
        this.tick();
        this.pos += 2;
        left = { kind: 'or', left, right: this.parseAnd() };
      } else return left;
    }
  }

  private parseAnd(): Expr {
    let left = this.parseUnary();
    for (;;) {
      this.skipWs();
      if (this.peek() === '&' && this.peek(1) === '&') {
        this.tick();
        this.pos += 2;
        left = { kind: 'and', left, right: this.parseUnary() };
      } else return left;
    }
  }

  private parseUnary(): Expr {
    this.skipWs();
    this.tick();
    const c = this.peek();
    if (c === '!') {
      this.pos++;
      return { kind: 'not', expr: this.parseUnary() };
    }
    if (c === '(') {
      this.pos++;
      const e = this.parseOr();
      this.skipWs();
      this.expect(')');
      return e;
    }
    return this.parseComparison();
  }

  private parseComparison(): Expr {
    const left = this.parseOperand();
    this.skipWs();
    const two = this.src.slice(this.pos, this.pos + 2);
    let op: CmpOp | null = null;
    if (two === '==' || two === '!=' || two === '<=' || two === '>=') op = two;
    else if (this.peek() === '<') op = '<';
    else if (this.peek() === '>') op = '>';
    if (op === null) return { kind: 'exists', expr: left };
    this.tick();
    this.pos += op.length;
    const right = this.parseOperand();
    return { kind: 'cmp', op, left, right };
  }

  private parseOperand(): Expr {
    this.skipWs();
    const c = this.peek();
    if (c === "'" || c === '"') return { kind: 'lit', value: this.parseQuoted() };
    if (c === '@' || c === '$') {
      this.pos++;
      const path = this.parseExprPath();
      return c === '@' ? { kind: 'cur', path } : { kind: 'root', path };
    }
    if (c === 't' && this.src.startsWith('true', this.pos)) {
      this.pos += 4;
      return { kind: 'lit', value: true };
    }
    if (c === 'f' && this.src.startsWith('false', this.pos)) {
      this.pos += 5;
      return { kind: 'lit', value: false };
    }
    if (c === 'n' && this.src.startsWith('null', this.pos)) {
      this.pos += 4;
      return { kind: 'lit', value: null };
    }
    const m = /^-?\d+(\.\d+)?([eE][+-]?\d+)?/.exec(this.src.slice(this.pos));
    if (m) {
      this.pos += m[0].length;
      return { kind: 'lit', value: Number(m[0]) };
    }
    this.error(`expected literal, '@' or '$' in filter expression`);
  }

  /** 谓词内只允许 child/index 导航（不允许再嵌套递归/切片/谓词）。 */
  private parseExprPath(): PathSegment[] {
    const segs: PathSegment[] = [];
    for (;;) {
      this.tick();
      const c = this.peek();
      if (c === '.') {
        this.pos++;
        segs.push({ kind: 'child', key: this.parseName() });
      } else if (c === '[') {
        this.pos++;
        this.skipWs();
        const q = this.peek();
        if (q === "'" || q === '"') {
          const key = this.parseQuoted();
          this.skipWs();
          this.expect(']');
          segs.push({ kind: 'child', key });
        } else {
          const n = this.tryParseInt();
          if (n === null) this.error('only string keys and integer indexes are allowed in filter paths');
          this.skipWs();
          this.expect(']');
          segs.push({ kind: 'index', index: n });
        }
      } else return segs;
    }
  }
}
