/**
 * A small parser for the SQL dialect produced by the renderer. Used to
 * verify rendered output by re-parsing it and walking the result in the
 * same deterministic traversal order as the renderer.
 */

export class ParseError extends Error {}

// Tokenizer -----------------------------------------------------------------

export type Token =
  | { readonly type: 'ident'; readonly value: string }
  | { readonly type: 'word'; readonly value: string }
  | { readonly type: 'placeholder'; readonly index: number }
  | { readonly type: 'number'; readonly text: string }
  | { readonly type: 'string'; readonly value: string }
  | { readonly type: 'punct'; readonly value: string }
  | { readonly type: 'op'; readonly value: string };

export function tokenize(sql: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  const n = sql.length;
  while (i < n) {
    const c = sql[i];
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') {
      i++;
      continue;
    }
    if (c === '-' && sql[i + 1] === '-') {
      while (i < n && sql[i] !== '\n') i++;
      continue;
    }
    if (c === '"') {
      let value = '';
      i++;
      for (;;) {
        if (i >= n) throw new ParseError('unterminated quoted identifier');
        if (sql[i] === '"') {
          if (sql[i + 1] === '"') {
            value += '"';
            i += 2;
          } else {
            i++;
            break;
          }
        } else {
          value += sql[i];
          i++;
        }
      }
      tokens.push({ type: 'ident', value });
      continue;
    }
    if (c === "'") {
      let value = '';
      i++;
      for (;;) {
        if (i >= n) throw new ParseError('unterminated string literal');
        if (sql[i] === "'") {
          if (sql[i + 1] === "'") {
            value += "'";
            i += 2;
          } else {
            i++;
            break;
          }
        } else {
          value += sql[i];
          i++;
        }
      }
      tokens.push({ type: 'string', value });
      continue;
    }
    if (c === '$') {
      let j = i + 1;
      while (j < n && sql[j] >= '0' && sql[j] <= '9') j++;
      if (j === i + 1) throw new ParseError(`bare '$' at offset ${i}`);
      tokens.push({ type: 'placeholder', index: Number(sql.slice(i + 1, j)) });
      i = j;
      continue;
    }
    if (c >= '0' && c <= '9') {
      let j = i;
      while (j < n && /[0-9.]/.test(sql[j])) j++;
      tokens.push({ type: 'number', text: sql.slice(i, j) });
      i = j;
      continue;
    }
    if (c === '(' || c === ')' || c === ',' || c === '.') {
      tokens.push({ type: 'punct', value: c });
      i++;
      continue;
    }
    if (c === '<' || c === '>' || c === '=') {
      const two = sql.slice(i, i + 2);
      if (two === '<=' || two === '>=' || two === '<>') {
        tokens.push({ type: 'op', value: two });
        i += 2;
      } else {
        tokens.push({ type: 'op', value: c });
        i++;
      }
      continue;
    }
    if (c === '*') {
      tokens.push({ type: 'op', value: '*' });
      i++;
      continue;
    }
    if (/[A-Za-z_]/.test(c)) {
      let j = i;
      while (j < n && /[A-Za-z0-9_]/.test(sql[j])) j++;
      tokens.push({ type: 'word', value: sql.slice(i, j) });
      i = j;
      continue;
    }
    throw new ParseError(`unexpected character ${JSON.stringify(c)} at offset ${i}`);
  }
  return tokens;
}

// Parsed AST ----------------------------------------------------------------

export type PExpr =
  | { readonly kind: 'placeholder'; readonly index: number }
  | { readonly kind: 'ident'; readonly path: readonly string[] }
  | { readonly kind: 'number'; readonly text: string }
  | { readonly kind: 'string'; readonly value: string }
  | { readonly kind: 'star' }
  | { readonly kind: 'call'; readonly name: string; readonly args: readonly PExpr[] }
  | { readonly kind: 'cmp'; readonly op: string; readonly left: PExpr; readonly right: PExpr }
  | { readonly kind: 'in'; readonly item: PExpr; readonly list: readonly PExpr[]; readonly negated: boolean }
  | { readonly kind: 'inSub'; readonly item: PExpr; readonly query: PSelect; readonly negated: boolean }
  | { readonly kind: 'and' | 'or'; readonly items: readonly PExpr[] }
  | { readonly kind: 'not'; readonly item: PExpr }
  | { readonly kind: 'scalarSub'; readonly query: PSelect }
  | { readonly kind: 'exists'; readonly query: PSelect };

export interface PColumn {
  readonly expr: PExpr;
  readonly alias: string | null;
}

export type PFrom =
  | { readonly kind: 'table'; readonly path: readonly string[]; readonly alias: string | null }
  | { readonly kind: 'subquery'; readonly query: PSelect; readonly alias: string };

export interface POrderItem {
  readonly expr: PExpr;
  readonly dir: 'ASC' | 'DESC';
}

export interface PSelect {
  readonly kind: 'select';
  readonly columns: readonly PColumn[];
  readonly from: readonly PFrom[];
  readonly where: PExpr | null;
  readonly orderBy: readonly POrderItem[];
  readonly limit: PExpr | null;
  readonly offset: PExpr | null;
}

// Recursive-descent parser ----------------------------------------------------

class Parser {
  private pos = 0;
  private readonly tokens: Token[];

  constructor(tokens: Token[]) {
    this.tokens = tokens;
  }

  private peek(): Token | undefined {
    return this.tokens[this.pos];
  }

  private atEnd(): boolean {
    return this.pos >= this.tokens.length;
  }

  private isWord(upper: string, offset = 0): boolean {
    const t = this.tokens[this.pos + offset];
    return t !== undefined && t.type === 'word' && t.value.toUpperCase() === upper;
  }

  private eatWord(upper: string): boolean {
    if (this.isWord(upper)) {
      this.pos++;
      return true;
    }
    return false;
  }

  private expectWord(upper: string): void {
    if (!this.eatWord(upper)) {
      throw new ParseError(`expected ${upper} at token ${this.pos}`);
    }
  }

  private isPunct(value: string): boolean {
    const t = this.peek();
    return t !== undefined && t.type === 'punct' && t.value === value;
  }

  private eatPunct(value: string): boolean {
    if (this.isPunct(value)) {
      this.pos++;
      return true;
    }
    return false;
  }

  private expectPunct(value: string): void {
    if (!this.eatPunct(value)) {
      throw new ParseError(`expected '${value}' at token ${this.pos}`);
    }
  }

  private name(): string {
    const t = this.tokens[this.pos];
    if (t === undefined) throw new ParseError('unexpected end of input');
    if (t.type === 'ident' || t.type === 'word') {
      this.pos++;
      return t.value;
    }
    throw new ParseError(`expected a name at token ${this.pos}`);
  }

  parseQuery(): PSelect {
    const q = this.parseSelect();
    if (!this.atEnd()) {
      throw new ParseError(`trailing tokens at ${this.pos}`);
    }
    return q;
  }

  parseSelect(): PSelect {
    this.expectWord('SELECT');
    const columns: PColumn[] = [];
    const first = this.peek();
    if (first !== undefined && first.type === 'op' && first.value === '*') {
      this.pos++;
      columns.push({ expr: { kind: 'star' }, alias: null });
    } else {
      for (;;) {
        const expr = this.parseExpr();
        let alias: string | null = null;
        if (this.eatWord('AS')) {
          alias = this.name();
        }
        columns.push({ expr, alias });
        if (!this.eatPunct(',')) break;
      }
    }
    const from: PFrom[] = [];
    if (this.eatWord('FROM')) {
      for (;;) {
        from.push(this.parseFrom());
        if (!this.eatPunct(',')) break;
      }
    }
    let where: PExpr | null = null;
    if (this.eatWord('WHERE')) {
      where = this.parseExpr();
    }
    const orderBy: POrderItem[] = [];
    if (this.isWord('ORDER')) {
      this.expectWord('ORDER');
      this.expectWord('BY');
      for (;;) {
        const expr = this.parseExpr();
        let dir: 'ASC' | 'DESC' = 'ASC';
        if (this.eatWord('DESC')) dir = 'DESC';
        else this.eatWord('ASC');
        orderBy.push({ expr, dir });
        if (!this.eatPunct(',')) break;
      }
    }
    let limit: PExpr | null = null;
    if (this.eatWord('LIMIT')) {
      limit = this.parsePrimary();
    }
    let offset: PExpr | null = null;
    if (this.eatWord('OFFSET')) {
      offset = this.parsePrimary();
    }
    return { kind: 'select', columns, from, where, orderBy, limit, offset };
  }

  private parseFrom(): PFrom {
    if (this.eatPunct('(')) {
      const query = this.parseSelect();
      this.expectPunct(')');
      this.expectWord('AS');
      const alias = this.name();
      return { kind: 'subquery', query, alias };
    }
    const path = [this.name()];
    while (this.eatPunct('.')) {
      path.push(this.name());
    }
    let alias: string | null = null;
    if (this.eatWord('AS')) {
      alias = this.name();
    }
    return { kind: 'table', path, alias };
  }

  parseExpr(): PExpr {
    return this.parseOr();
  }

  private parseOr(): PExpr {
    const items = [this.parseAnd()];
    while (this.eatWord('OR')) {
      items.push(this.parseAnd());
    }
    return items.length === 1 ? items[0] : { kind: 'or', items };
  }

  private parseAnd(): PExpr {
    const items = [this.parseNot()];
    while (this.eatWord('AND')) {
      items.push(this.parseNot());
    }
    return items.length === 1 ? items[0] : { kind: 'and', items };
  }

  private parseNot(): PExpr {
    if (this.isWord('NOT')) {
      this.pos++;
      return { kind: 'not', item: this.parseNot() };
    }
    return this.parsePredicate();
  }

  private parsePredicate(): PExpr {
    const left = this.parsePrimary();
    let negated = false;
    if (this.isWord('NOT') && this.isWord('IN', 1)) {
      this.pos++;
      negated = true;
    }
    if (this.eatWord('IN')) {
      this.expectPunct('(');
      if (this.isWord('SELECT')) {
        const query = this.parseSelect();
        this.expectPunct(')');
        return { kind: 'inSub', item: left, query, negated };
      }
      const list: PExpr[] = [];
      for (;;) {
        list.push(this.parseExpr());
        if (!this.eatPunct(',')) break;
      }
      this.expectPunct(')');
      return { kind: 'in', item: left, list, negated };
    }
    if (negated) {
      throw new ParseError(`expected IN after NOT at token ${this.pos}`);
    }
    const t = this.peek();
    if (t !== undefined && t.type === 'op' && t.value !== '*') {
      this.pos++;
      const right = this.parsePrimary();
      return { kind: 'cmp', op: t.value, left, right };
    }
    if (this.isWord('LIKE')) {
      this.pos++;
      const right = this.parsePrimary();
      return { kind: 'cmp', op: 'LIKE', left, right };
    }
    return left;
  }

  private parsePrimary(): PExpr {
    const t = this.peek();
    if (t === undefined) throw new ParseError('unexpected end of input');
    switch (t.type) {
      case 'placeholder':
        this.pos++;
        return { kind: 'placeholder', index: t.index };
      case 'number':
        this.pos++;
        return { kind: 'number', text: t.text };
      case 'string':
        this.pos++;
        return { kind: 'string', value: t.value };
      case 'op':
        if (t.value === '*') {
          this.pos++;
          return { kind: 'star' };
        }
        throw new ParseError(`unexpected operator ${t.value}`);
      case 'ident': {
        this.pos++;
        const path = [t.value];
        while (this.eatPunct('.')) {
          path.push(this.name());
        }
        return { kind: 'ident', path };
      }
      case 'word': {
        const upper = t.value.toUpperCase();
        if (upper === 'EXISTS') {
          this.pos++;
          this.expectPunct('(');
          const query = this.parseSelect();
          this.expectPunct(')');
          return { kind: 'exists', query };
        }
        const ahead = this.tokens[this.pos + 1];
        if (ahead !== undefined && ahead.type === 'punct' && ahead.value === '(') {
          this.pos += 2;
          const args: PExpr[] = [];
          if (!this.isPunct(')')) {
            for (;;) {
              args.push(this.parseExpr());
              if (!this.eatPunct(',')) break;
            }
          }
          this.expectPunct(')');
          return { kind: 'call', name: t.value, args };
        }
        this.pos++;
        const path = [t.value];
        while (this.eatPunct('.')) {
          path.push(this.name());
        }
        return { kind: 'ident', path };
      }
      case 'punct': {
        if (t.value === '(') {
          this.pos++;
          if (this.isWord('SELECT')) {
            const query = this.parseSelect();
            this.expectPunct(')');
            return { kind: 'scalarSub', query };
          }
          const inner = this.parseExpr();
          this.expectPunct(')');
          return inner;
        }
        throw new ParseError(`unexpected '${t.value}' at token ${this.pos}`);
      }
    }
  }
}

export function parseSelect(sql: string): PSelect {
  return new Parser(tokenize(sql)).parseQuery();
}
