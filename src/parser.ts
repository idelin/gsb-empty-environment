import type { Expr, Query, Segment, SimpleSegment, CmpOp, JsonValue } from "./ast.ts";
import type { Budget } from "./budget.ts";
import { QuerySyntaxError } from "./errors.ts";

const NAME_START = /[A-Za-z_$]/;
const NAME_CHAR = /[A-Za-z0-9_$-]/;
const DIGIT = /[0-9]/;

/**
 * Scannerless recursive-descent parser for the query language:
 *
 *   query    := '$' segment*
 *   segment  := '.' name | '.*' | '..' (name | '*' | bracket) | bracket
 *   bracket  := '[' (string | int | slice | '*' | '?' '(' expr ')') ']'
 *   slice    := int? ':' int? (':' int?)?
 *   expr     := orExpr
 *   orExpr   := andExpr ('||' andExpr)*
 *   andExpr  := unary ('&&' unary)*
 *   unary    := '!' unary | '(' expr ')' | comparison
 *   comparison := primary (cmpOp primary)?
 *   primary  := '@' suffix* | number | string | 'true' | 'false' | 'null'
 */
export function parseQuery(source: string, budget?: Budget): Query {
  return new Parser(source, budget).parse();
}

class Parser {
  private pos = 0;
  private readonly src: string;
  private readonly budget?: Budget;

  constructor(src: string, budget?: Budget) {
    this.src = src;
    this.budget = budget;
  }

  parse(): Query {
    this.skipWs();
    this.expect("$", "query must start with '$'");
    const segments: Segment[] = [];
    while (!this.eof()) {
      this.skipWs();
      if (this.eof()) break;
      if (this.peek() === "." && this.peek(1) === ".") {
        this.advance(2);
        segments.push({ kind: "recursive", inner: this.parseRecursiveTarget() });
      } else if (this.peek() === ".") {
        this.advance(1);
        if (this.peek() === "*") {
          this.advance(1);
          segments.push({ kind: "wildcard" });
        } else {
          segments.push({ kind: "field", name: this.parseName() });
        }
      } else if (this.peek() === "[") {
        segments.push(this.parseBracket());
      } else {
        throw this.error(`unexpected character ${JSON.stringify(this.peek())}`);
      }
    }
    return { source: this.src, segments };
  }

  private parseRecursiveTarget(): SimpleSegment {
    if (this.eof()) throw this.error("expected a selector after '..'");
    const ch = this.peek();
    if (ch === "*") {
      this.advance(1);
      return { kind: "wildcard" };
    }
    if (ch === "[") return this.parseBracket();
    return { kind: "field", name: this.parseName() };
  }

  private parseBracket(): SimpleSegment {
    this.expect("[");
    this.skipWs();
    const ch = this.peek();
    if (ch === "*") {
      this.advance(1);
      this.closeBracket();
      return { kind: "wildcard" };
    }
    if (ch === "?") {
      this.advance(1);
      this.skipWs();
      this.expect("(", "expected '(' after '?'");
      const expr = this.parseExpr();
      this.skipWs();
      this.expect(")", "expected ')' to close filter");
      this.closeBracket();
      return { kind: "filter", expr };
    }
    if (ch === "'" || ch === '"') {
      const name = this.parseString();
      if (typeof name !== "string") throw this.error("expected string key");
      this.closeBracket();
      return { kind: "field", name };
    }
    // index or slice
    const first = this.parseOptInt();
    this.skipWs();
    if (this.peek() === ":") {
      this.advance(1);
      this.skipWs();
      const second = this.parseOptInt();
      this.skipWs();
      let step: number | null = null;
      if (this.peek() === ":") {
        this.advance(1);
        this.skipWs();
        step = this.parseOptInt();
        if (step === 0) throw this.error("slice step must not be 0");
        this.skipWs();
      }
      if (first === null && second === null && step === null) {
        throw this.error("empty slice; use [*] for wildcard");
      }
      this.closeBracket();
      return { kind: "slice", start: first, end: second, step };
    }
    if (first === null) throw this.error("expected index, slice, string, '*' or filter");
    this.closeBracket();
    return { kind: "index", index: first };
  }

  private closeBracket(): void {
    this.skipWs();
    this.expect("]", "expected ']'");
  }

  private parseExpr(): Expr {
    return this.parseOr();
  }

  private parseOr(): Expr {
    let left = this.parseAnd();
    for (;;) {
      this.skipWs();
      if (this.peek() !== "|" || this.peek(1) !== "|") return left;
      this.advance(2);
      left = { kind: "or", left, right: this.parseAnd() };
    }
  }

  private parseAnd(): Expr {
    let left = this.parseUnary();
    for (;;) {
      this.skipWs();
      if (this.peek() !== "&" || this.peek(1) !== "&") return left;
      this.advance(2);
      left = { kind: "and", left, right: this.parseUnary() };
    }
  }

  private parseUnary(): Expr {
    this.skipWs();
    if (this.peek() === "!") {
      this.advance(1);
      return { kind: "not", operand: this.parseUnary() };
    }
    if (this.peek() === "(") {
      this.advance(1);
      const inner = this.parseExpr();
      this.skipWs();
      this.expect(")", "expected ')'");
      return inner;
    }
    return this.parseComparison();
  }

  private parseComparison(): Expr {
    const left = this.parsePrimary();
    this.skipWs();
    const op = this.parseCmpOp();
    if (op === null) return left;
    const right = this.parsePrimary();
    return { kind: "cmp", op, left, right };
  }

  private parseCmpOp(): CmpOp | null {
    const two = this.src.slice(this.pos, this.pos + 2);
    if (two === "==" || two === "!=" || two === "<=" || two === ">=") {
      this.advance(2);
      return two;
    }
    const one = this.peek();
    if (one === "<" || one === ">") {
      this.advance(1);
      return one;
    }
    return null;
  }

  private parsePrimary(): Expr {
    this.skipWs();
    const ch = this.peek();
    if (ch === "@") {
      this.advance(1);
      return { kind: "current", path: this.parseCurrentPath() };
    }
    if (ch === "'" || ch === '"') {
      return { kind: "literal", value: this.parseString() };
    }
    if (ch === "-" || DIGIT.test(ch)) {
      return { kind: "literal", value: this.parseNumber() };
    }
    for (const kw of ["true", "false", "null"] as const) {
      if (this.src.startsWith(kw, this.pos)) {
        this.advance(kw.length);
        const value: JsonValue = kw === "true" ? true : kw === "false" ? false : null;
        return { kind: "literal", value };
      }
    }
    throw this.error("expected '@', literal or '(' in filter expression");
  }

  private parseCurrentPath(): (string | number)[] {
    const path: (string | number)[] = [];
    for (;;) {
      if (this.peek() === "." && this.peek(1) !== ".") {
        this.advance(1);
        path.push(this.parseName());
      } else if (this.peek() === "[") {
        this.advance(1);
        this.skipWs();
        const ch = this.peek();
        if (ch === "'" || ch === '"') {
          path.push(this.parseString());
        } else {
          const n = this.parseOptInt();
          if (n === null) throw this.error("expected string or integer inside '[...]'");
          path.push(n);
        }
        this.closeBracket();
      } else {
        return path;
      }
    }
  }

  private parseName(): string {
    if (this.eof() || !NAME_START.test(this.peek())) {
      throw this.error("expected a name");
    }
    let name = "";
    while (!this.eof() && NAME_CHAR.test(this.peek())) {
      name += this.peek();
      this.advance(1);
    }
    return name;
  }

  private parseOptInt(): number | null {
    this.skipWs();
    const start = this.pos;
    if (this.peek() === "-") this.advance(1);
    let digits = "";
    while (!this.eof() && DIGIT.test(this.peek())) {
      digits += this.peek();
      this.advance(1);
    }
    if (digits === "") {
      this.pos = start;
      return null;
    }
    return Number(this.src.slice(start, this.pos));
  }

  private parseNumber(): number {
    const start = this.pos;
    if (this.peek() === "-") this.advance(1);
    while (!this.eof() && DIGIT.test(this.peek())) this.advance(1);
    if (this.peek() === ".") {
      this.advance(1);
      while (!this.eof() && DIGIT.test(this.peek())) this.advance(1);
    }
    if (this.peek() === "e" || this.peek() === "E") {
      this.advance(1);
      if (this.peek() === "+" || this.peek() === "-") this.advance(1);
      while (!this.eof() && DIGIT.test(this.peek())) this.advance(1);
    }
    const text = this.src.slice(start, this.pos);
    const n = Number(text);
    if (text === "" || text === "-" || Number.isNaN(n)) {
      throw this.error(`invalid number ${JSON.stringify(text)}`);
    }
    return n;
  }

  private parseString(): string {
    const quote = this.peek();
    this.advance(1);
    let out = "";
    for (;;) {
      if (this.eof()) throw this.error("unterminated string literal");
      const ch = this.peek();
      if (ch === quote) {
        this.advance(1);
        return out;
      }
      if (ch === "\\") {
        this.advance(1);
        if (this.eof()) throw this.error("unterminated escape sequence");
        const esc = this.peek();
        this.advance(1);
        switch (esc) {
          case "n": out += "\n"; break;
          case "t": out += "\t"; break;
          case "r": out += "\r"; break;
          case "b": out += "\b"; break;
          case "f": out += "\f"; break;
          case "/": out += "/"; break;
          case "\\": out += "\\"; break;
          case "'": out += "'"; break;
          case '"': out += '"'; break;
          case "u": {
            const hex = this.src.slice(this.pos, this.pos + 4);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) throw this.error("invalid \\u escape");
            out += String.fromCharCode(parseInt(hex, 16));
            this.advance(4);
            break;
          }
          default:
            throw this.error(`unknown escape '\\${esc}'`);
        }
      } else {
        out += ch;
        this.advance(1);
      }
    }
  }

  private skipWs(): void {
    while (!this.eof() && (this.peek() === " " || this.peek() === "\t" || this.peek() === "\n")) {
      this.advance(1);
    }
  }

  private peek(offset = 0): string {
    return this.src[this.pos + offset] ?? "";
  }

  private advance(n: number): void {
    this.budget?.charge("parse", n);
    this.pos += n;
  }

  private eof(): boolean {
    return this.pos >= this.src.length;
  }

  private expect(ch: string, message?: string): void {
    if (this.peek() !== ch) {
      throw this.error(message ?? `expected ${JSON.stringify(ch)}`);
    }
    this.advance(ch.length);
  }

  private error(message: string): QuerySyntaxError {
    return new QuerySyntaxError(message, this.pos, this.src);
  }
}
