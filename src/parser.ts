// 递归下降 SQL 解析器（SELECT 子集）

import type {
  Expr,
  SelectItem,
  SelectStmt,
  TableSource,
  BinaryOp,
} from './ast.ts';
import { tokenize, type Token } from './lexer.ts';

const COMPARISON_OPS = new Set(['=', '<>', '!=', '<', '<=', '>', '>=']);

class Parser {
  private tokens: Token[];
  private pos = 0;

  constructor(sql: string) {
    this.tokens = tokenize(sql);
  }

  private peek(): Token {
    return this.tokens[this.pos];
  }

  private next(): Token {
    return this.tokens[this.pos++];
  }

  private isSymbol(v: string): boolean {
    const t = this.peek();
    return t.type === 'symbol' && t.value === v;
  }

  private isKeyword(v: string): boolean {
    const t = this.peek();
    return t.type === 'ident' && t.value.toUpperCase() === v;
  }

  private expectSymbol(v: string): void {
    if (!this.isSymbol(v)) {
      throw new SyntaxError(
        `期望 '${v}'，实际得到 '${this.peek().value}'（位置 ${this.peek().pos}）`,
      );
    }
    this.next();
  }

  private expectKeyword(v: string): void {
    if (!this.isKeyword(v)) {
      throw new SyntaxError(
        `期望关键字 ${v}，实际得到 '${this.peek().value}'（位置 ${this.peek().pos}）`,
      );
    }
    this.next();
  }

  parseStatement(): SelectStmt {
    const stmt = this.parseSelect();
    if (this.isSymbol(';')) this.next();
    if (this.peek().type !== 'eof') {
      throw new SyntaxError(`语句结束后存在多余内容：'${this.peek().value}'`);
    }
    return stmt;
  }

  parseSelect(): SelectStmt {
    this.expectKeyword('SELECT');
    const columns: SelectItem[] = [];
    do {
      columns.push(this.parseSelectItem());
    } while (this.isSymbol(',') && (this.next(), true));

    this.expectKeyword('FROM');
    const from: TableSource[] = [];
    do {
      from.push(this.parseTableSource());
    } while (this.isSymbol(',') && (this.next(), true));

    let where: Expr | undefined;
    if (this.isKeyword('WHERE')) {
      this.next();
      where = this.parseExpr();
    }
    return { kind: 'select', columns, from, where };
  }

  private parseSelectItem(): SelectItem {
    if (this.isSymbol('*')) {
      this.next();
      return { kind: 'star' };
    }
    // t.*
    if (
      this.peek().type === 'ident' &&
      this.tokens[this.pos + 1]?.type === 'symbol' &&
      this.tokens[this.pos + 1]?.value === '.' &&
      this.tokens[this.pos + 2]?.type === 'symbol' &&
      this.tokens[this.pos + 2]?.value === '*'
    ) {
      const table = this.next().value;
      this.next(); // .
      this.next(); // *
      return { kind: 'star', table };
    }
    const expr = this.parseExpr();
    let alias: string | undefined;
    if (this.isKeyword('AS')) {
      this.next();
      alias = this.next().value;
    }
    return { kind: 'expr', expr, alias };
  }

  private parseTableSource(): TableSource {
    let source = this.parseTableFactor();
    // 支持输入中已存在的 [INNER|SEMI|ANTI|CROSS] JOIN ... ON ...
    for (;;) {
      let joinType: 'INNER' | 'SEMI' | 'ANTI' | 'CROSS' = 'INNER';
      if (this.isKeyword('JOIN')) {
        // 裸 JOIN 视为 INNER
      } else if (this.isKeyword('INNER') || this.isKeyword('SEMI') || this.isKeyword('ANTI') || this.isKeyword('CROSS')) {
        joinType = this.next().value.toUpperCase() as typeof joinType;
        if (!this.isKeyword('JOIN')) {
          this.pos--; // 不是 JOIN 短语，回退
          break;
        }
      } else {
        break;
      }
      this.next(); // JOIN
      const right = this.parseTableFactor();
      if (joinType === 'CROSS') {
        source = { kind: 'join', joinType, left: source, right };
      } else {
        this.expectKeyword('ON');
        const on = this.parseExpr();
        source = { kind: 'join', joinType, left: source, right, on };
      }
    }
    return source;
  }

  private parseTableFactor(): TableSource {
    if (this.isSymbol('(')) {
      this.next();
      const sub = this.parseSelect();
      this.expectSymbol(')');
      let alias: string | undefined;
      if (this.isKeyword('AS')) this.next();
      if (this.peek().type === 'ident') alias = this.next().value;
      // 派生表：包一层单表 select 的 from 列表语义过重，这里直接拒绝，
      // 本组件聚焦于相关子查询解除，派生表留作扩展点。
      throw new SyntaxError(`暂不支持 FROM 中的派生表（别名 ${alias ?? '?'}）`);
    }
    const name = this.next().value;
    let alias: string | undefined;
    if (this.isKeyword('AS')) {
      this.next();
      alias = this.next().value;
    } else if (
      this.peek().type === 'ident' &&
      !['WHERE', 'ON', 'JOIN', 'INNER', 'SEMI', 'ANTI', 'CROSS', 'AND', 'OR'].includes(
        this.peek().value.toUpperCase(),
      )
    ) {
      alias = this.next().value;
    }
    return { kind: 'table', name, alias };
  }

  parseExpr(): Expr {
    return this.parseOr();
  }

  private parseOr(): Expr {
    let left = this.parseAnd();
    while (this.isKeyword('OR')) {
      this.next();
      left = { kind: 'binary', op: 'OR', left, right: this.parseAnd() };
    }
    return left;
  }

  private parseAnd(): Expr {
    let left = this.parseNot();
    while (this.isKeyword('AND')) {
      this.next();
      left = { kind: 'binary', op: 'AND', left, right: this.parseNot() };
    }
    return left;
  }

  private parseNot(): Expr {
    if (this.isKeyword('NOT')) {
      this.next();
      // NOT EXISTS (...)
      if (this.isKeyword('EXISTS')) {
        this.next();
        return { kind: 'exists', negated: true, subquery: this.parseParenSubquery() };
      }
      throw new SyntaxError(`NOT 后仅支持 EXISTS（位置 ${this.peek().pos}）`);
    }
    return this.parsePredicate();
  }

  private parsePredicate(): Expr {
    if (this.isKeyword('EXISTS')) {
      this.next();
      return { kind: 'exists', negated: false, subquery: this.parseParenSubquery() };
    }
    const left = this.parsePrimary();

    // expr [NOT] IN (subquery)
    const nextIsIn = this.tokens[this.pos + 1]?.value.toUpperCase() === 'IN';
    if (this.isKeyword('IN') || (this.isKeyword('NOT') && nextIsIn)) {
      let negated = false;
      if (this.isKeyword('NOT')) {
        this.next();
        negated = true;
      }
      this.next(); // IN
      return { kind: 'in', negated, expr: left, subquery: this.parseParenSubquery() };
    }

    const t = this.peek();
    if (t.type === 'symbol' && COMPARISON_OPS.has(t.value)) {
      this.next();
      const right = this.parsePrimary();
      return { kind: 'binary', op: t.value as BinaryOp, left, right };
    }
    return left;
  }

  private parseParenSubquery(): SelectStmt {
    this.expectSymbol('(');
    const sub = this.parseSelect();
    this.expectSymbol(')');
    return sub;
  }

  private parsePrimary(): Expr {
    const t = this.peek();
    if (t.type === 'number') {
      this.next();
      return { kind: 'literal', value: Number(t.value) };
    }
    if (t.type === 'string') {
      this.next();
      return { kind: 'literal', value: t.value };
    }
    if (t.type === 'ident') {
      const upper = t.value.toUpperCase();
      if (upper === 'NULL') {
        this.next();
        return { kind: 'literal', value: null };
      }
      if (upper === 'TRUE') {
        this.next();
        return { kind: 'literal', value: true };
      }
      // 列引用：a.b 或 b
      this.next();
      if (this.isSymbol('.')) {
        this.next();
        const col = this.next().value;
        return { kind: 'column', table: t.value, name: col };
      }
      return { kind: 'column', name: t.value };
    }
    if (this.isSymbol('(')) {
      this.next();
      const inner = this.parseExpr();
      this.expectSymbol(')');
      return inner;
    }
    throw new SyntaxError(`意外的记号 '${t.value}'（位置 ${t.pos}）`);
  }
}

export function parse(sql: string): SelectStmt {
  return new Parser(sql).parseStatement();
}
