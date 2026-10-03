/**
 * Minimal SQL parser for the demo grammar:
 *   SELECT <exprs> FROM <table> <alias>
 *     [JOIN <table> <alias> ON <a.col> = <b.col>]
 *     [WHERE <predicate>]
 *     [GROUP BY <cols>]
 *     [ORDER BY <col> [ASC|DESC], ...]
 * Produces a LogicalNode. Identifiers are qualified as alias.column.
 */
import type { LogicalNode } from './plan.ts';
import { toOrdering, type Direction, type Ordering } from './properties.ts';

function tokenize(sql: string): string[] {
  const tokens = sql.match(/[A-Za-z_][A-Za-z0-9_.]*|\d+|>=|<=|<>|!=|[=<>(),*]/g);
  if (!tokens) throw new Error(`cannot tokenize: ${sql}`);
  return tokens;
}

class Parser {
  private pos = 0;
  private tokens: string[];

  constructor(tokens: string[]) {
    this.tokens = tokens;
  }

  private peek(): string {
    return this.tokens[this.pos] ?? '';
  }

  private next(): string {
    const tok = this.tokens[this.pos++];
    if (tok === undefined) throw new Error('unexpected end of query');
    return tok;
  }

  private expect(keyword: string): void {
    const tok = this.next();
    if (tok.toUpperCase() !== keyword)
      throw new Error(`expected ${keyword}, got "${tok}"`);
  }

  private atKeyword(keyword: string): boolean {
    return this.peek().toUpperCase() === keyword;
  }

  private readList(terminators: string[]): string[] {
    const items: string[] = [];
    let depth = 0;
    let current = '';
    while (this.pos < this.tokens.length) {
      const tok = this.peek();
      if (tok === '(') depth++;
      if (tok === ')') depth--;
      if (depth === 0 && terminators.some((t) => tok.toUpperCase() === t)) break;
      if (depth === 0 && tok === ',') {
        items.push(current.trim());
        current = '';
        this.next();
        continue;
      }
      current += (current && /[A-Za-z0-9_(]/.test(tok[0]) ? ' ' : '') + tok;
      this.next();
    }
    if (current.trim()) items.push(current.trim());
    return items;
  }

  parseQuery(): LogicalNode {
    this.expect('SELECT');
    const selectList = this.readList(['FROM']);
    this.expect('FROM');

    const table = this.next();
    const alias = this.next();
    let node: LogicalNode = { kind: 'Scan', table, alias };

    if (this.atKeyword('JOIN')) {
      this.next();
      const rTable = this.next();
      const rAlias = this.next();
      this.expect('ON');
      const leftKey = this.next();
      this.expect('=');
      const rightKey = this.next();
      node = {
        kind: 'Join',
        left: node,
        right: { kind: 'Scan', table: rTable, alias: rAlias },
        leftKey,
        rightKey,
      };
    }

    if (this.atKeyword('WHERE')) {
      this.next();
      const [predicate] = this.readList(['GROUP', 'ORDER']);
      node = { kind: 'Filter', predicate, child: node };
    }

    if (this.atKeyword('GROUP')) {
      this.next();
      this.expect('BY');
      const keys = this.readList(['ORDER']);
      const aggregates = selectList.filter((e) => e.includes('('));
      node = { kind: 'GroupBy', keys, aggregates, child: node };
    }

    if (this.atKeyword('ORDER')) {
      this.next();
      this.expect('BY');
      const keys: Ordering = [];
      while (this.pos < this.tokens.length) {
        const column = this.next();
        let direction: Direction = 'asc';
        if (this.atKeyword('ASC')) this.next();
        else if (this.atKeyword('DESC')) {
          this.next();
          direction = 'desc';
        }
        keys.push({ column, direction });
        if (this.peek() === ',') this.next();
        else break;
      }
      node = { kind: 'OrderBy', keys, child: node };
    }

    const columns = selectList.filter((e) => !e.includes('('));
    if (columns.length > 0 && !(columns.length === 1 && columns[0] === '*')) {
      node = { kind: 'Project', columns, child: node };
    }
    return node;
  }
}

export function parse(sql: string): LogicalNode {
  return new Parser(tokenize(sql)).parseQuery();
}
