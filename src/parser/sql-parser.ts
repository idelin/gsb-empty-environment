import { ALLOWED_FUNCTIONS } from '../ast.ts';
import type {
  CExpr,
  CJoin,
  COrder,
  CRelation,
  CSelect,
  CSelectItem,
} from '../canonical.ts';
import { QueryStructureError } from '../errors.ts';
import { tokenize } from './tokenizer.ts';
import type { Token } from './tokenizer.ts';

const JOIN_KEYWORDS: ReadonlySet<string> = new Set(['INNER', 'LEFT', 'RIGHT', 'FULL']);
const COMPARISON_OPS = new Set(['=', '<>', '<', '>', '<=', '>=']);

export class SqlParser {
  private readonly tokens: Token[];
  private index = 0;

  constructor(sql: string) {
    this.tokens = tokenize(sql);
  }

  parseSelectStatement(): CSelect {
    const select = this.parseSelect();
    if (this.index !== this.tokens.length) {
      throw new QueryStructureError(`unexpected trailing token: ${this.peek().value}`);
    }
    return select;
  }

  private parseSelect(): CSelect {
    this.expectWord('SELECT');
    const distinct = this.consumeWord('DISTINCT');
    const selectList = this.parseSelectList();

    this.expectWord('FROM');
    const from = this.parseRelation();
    const joins: CJoin[] = [];
    while (JOIN_KEYWORDS.has(this.peekWord())) {
      joins.push(this.parseJoin());
    }

    let where: CExpr | null = null;
    if (this.consumeWord('WHERE')) {
      where = this.parseExpr();
    }

    const groupBy: CExpr[] = [];
    if (this.consumeWord('GROUP')) {
      this.expectWord('BY');
      do {
        groupBy.push(this.parseExpr());
      } while (this.consumePunct(','));
    }

    let having: CExpr | null = null;
    if (this.consumeWord('HAVING')) {
      having = this.parseExpr();
    }

    const orderBy: COrder[] = [];
    if (this.consumeWord('ORDER')) {
      this.expectWord('BY');
      do {
        const expr = this.parseExpr();
        let direction: 'asc' | 'desc' = 'asc';
        if (this.consumeWord('DESC')) {
          direction = 'desc';
        } else if (this.consumeWord('ASC')) {
          direction = 'asc';
        }
        orderBy.push({ expr, direction });
      } while (this.consumePunct(','));
    }

    let limit: number | null = null;
    if (this.consumeWord('LIMIT')) {
      limit = this.parseLimit();
    }

    return {
      kind: 'select',
      distinct,
      selectList,
      from,
      joins,
      where,
      groupBy,
      having,
      orderBy,
      limit,
    };
  }

  private parseSelectList(): CSelectItem[] {
    const items: CSelectItem[] = [];
    do {
      if (this.isStarToken()) {
        this.advance();
        items.push({ star: true, qualifier: null });
        continue;
      }
      if (this.isQualifiedStar()) {
        const qualifier = this.advance().value;
        this.advance();
        this.advance();
        items.push({ star: true, qualifier });
        continue;
      }
      const expr = this.parseExpr();
      let alias: string | null = null;
      if (this.consumeWord('AS')) {
        alias = this.parseQuotedName();
      }
      items.push({ star: false, expr, alias });
    } while (this.consumePunct(','));
    return items;
  }

  private isStarToken(): boolean {
    const token = this.peek();
    return token.type === 'punct' && token.value === '*';
  }

  private isQualifiedStar(): boolean {
    return (
      this.peek().type === 'quoted' &&
      this.peekAt(1)?.type === 'punct' &&
      this.peekAt(1)?.value === '.' &&
      this.peekAt(2)?.type === 'punct' &&
      this.peekAt(2)?.value === '*'
    );
  }

  private parseRelation(): CRelation {
    if (this.peek().type === 'punct' && this.peek().value === '(') {
      const query = this.parseParenthesizedSelect();
      this.expectWord('AS');
      const alias = this.parseQuotedName();
      return { kind: 'subquery', query, alias };
    }
    const first = this.parseQuotedName();
    let schema: string | null = null;
    let name = first;
    if (!this.atEnd() && this.peek().type === 'punct' && this.peek().value === '.') {
      this.advance();
      schema = first;
      name = this.parseQuotedName();
    }
    let alias: string | null = null;
    if (this.consumeWord('AS')) {
      alias = this.parseQuotedName();
    }
    return { kind: 'table', schema, name, alias };
  }

  private parseJoin(): CJoin {
    const keyword = this.advance().value.toUpperCase();
    this.expectWord('JOIN');
    const type =
      keyword === 'INNER'
        ? 'inner'
        : keyword === 'LEFT'
          ? 'left'
          : keyword === 'RIGHT'
            ? 'right'
            : 'full';
    const relation = this.parseRelation();
    this.expectWord('ON');
    const on = this.parseExpr();
    return { type, relation, on };
  }

  private parseParenthesizedSelect(): CSelect {
    this.expectPunct('(');
    const select = this.parseSelect();
    this.expectPunct(')');
    return select;
  }

  private parseLimit(): number {
    const token = this.peek();
    if (token.type !== 'number') {
      throw new QueryStructureError('LIMIT must be a non-negative integer literal');
    }
    this.advance();
    const value = Number(token.value);
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new QueryStructureError('LIMIT must be a non-negative integer literal');
    }
    return value;
  }

  private parseQuotedName(): string {
    const token = this.peek();
    if (token.type !== 'quoted') {
      throw new QueryStructureError(`expected quoted identifier, found: ${token.value}`);
    }
    this.advance();
    return token.value;
  }

  private atEnd(): boolean {
    return this.index >= this.tokens.length;
  }

  private peek(): Token {
    const token = this.tokens[this.index];
    if (!token) {
      throw new QueryStructureError('unexpected end of rendered SQL');
    }
    return token;
  }

  private peekAt(offset: number): Token | undefined {
    return this.tokens[this.index + offset];
  }

  private peekWord(): string {
    const token = this.tokens[this.index];
    return token?.type === 'word' ? token.value.toUpperCase() : '';
  }

  private advance(): Token {
    const token = this.peek();
    this.index += 1;
    return token;
  }

  private consumeWord(expected: string): boolean {
    if (this.peekWord() === expected) {
      this.advance();
      return true;
    }
    return false;
  }

  private expectWord(expected: string): void {
    if (!this.consumeWord(expected)) {
      throw new QueryStructureError(`expected keyword ${expected}, found: ${this.peek().value}`);
    }
  }

  private consumePunct(expected: string): boolean {
    const token = this.tokens[this.index];
    if (token?.type === 'punct' && token.value === expected) {
      this.advance();
      return true;
    }
    return false;
  }

  private expectPunct(expected: string): void {
    if (!this.consumePunct(expected)) {
      throw new QueryStructureError(`expected ${expected}, found: ${this.peek().value}`);
    }
  }

  private parseExpr(): CExpr {
    return this.parseOr();
  }

  private parseOr(): CExpr {
    let left = this.parseAnd();
    while (this.consumeWord('OR')) {
      const right = this.parseAnd();
      left = { kind: 'bin', op: 'OR', left, right };
    }
    return left;
  }

  private parseAnd(): CExpr {
    let left = this.parseNot();
    while (this.consumeWord('AND')) {
      const right = this.parseNot();
      left = { kind: 'bin', op: 'AND', left, right };
    }
    return left;
  }

  private parseNot(): CExpr {
    if (this.consumeWord('NOT')) {
      return { kind: 'un', op: 'NOT', expr: this.parseNot() };
    }
    return this.parseComparison();
  }

  private parseComparison(): CExpr {
    let left = this.parseUnary();
    for (;;) {
      if (this.atEnd()) {
        return left;
      }
      if (this.consumeWord('IS')) {
        const negated = this.consumeWord('NOT');
        this.expectWord('NULL');
        const isNull: CExpr = { kind: 'isNull', expr: left };
        left = negated ? { kind: 'un', op: 'NOT', expr: isNull } : isNull;
        continue;
      }
      if (this.peekWord() === 'IN' || this.peekWord() === 'NOT' || this.peekWord() === 'BETWEEN') {
        left = this.parseInOrBetween(left);
        continue;
      }
      const token = this.peek();
      if (token.type === 'punct' && COMPARISON_OPS.has(token.value)) {
        this.advance();
        const right = this.parseUnary();
        left = {
          kind: 'bin',
          op: token.value as '=' | '<>' | '<' | '>' | '<=' | '>=',
          left,
          right,
        };
        continue;
      }
      if (this.consumeWord('LIKE')) {
        const right = this.parseUnary();
        left = { kind: 'bin', op: 'LIKE', left, right };
        continue;
      }
      return left;
    }
  }

  private parseInOrBetween(left: CExpr): CExpr {
    const negated = this.consumeWord('NOT');
    if (this.consumeWord('IN')) {
      this.expectPunct('(');
      if (this.peekWord() === 'SELECT') {
        const query = this.parseSelect();
        this.expectPunct(')');
        return { kind: 'inQuery', expr: left, query, negated };
      }
      const slots: number[] = [];
      do {
        const token = this.peek();
        if (token.type === 'probe') {
          this.advance();
          slots.push(-1);
        } else {
          slots.push(this.parseParamSlot());
        }
      } while (this.consumePunct(','));
      this.expectPunct(')');
      return { kind: 'inList', expr: left, slots, negated };
    }
    this.expectWord('BETWEEN');
    const low = this.parseUnary();
    this.expectWord('AND');
    const high = this.parseUnary();
    return { kind: 'between', expr: left, low, high, negated };
  }

  private parseUnary(): CExpr {
    if (this.consumePunct('-')) {
      return { kind: 'un', op: '-', expr: this.parseUnary() };
    }
    return this.parsePrimary();
  }

  private parsePrimary(): CExpr {
    const token = this.peek();
    if (token.type === 'probe') {
      this.advance();
      return { kind: 'lit', value: null };
    }
    if (token.type === 'punct' && token.value === '(') {
      if (this.peekAt(1)?.type === 'word' && this.peekAt(1)?.value === 'SELECT') {
        const query = this.parseParenthesizedSelect();
        return { kind: 'scalarQuery', query };
      }
      this.advance();
      const inner = this.parseExpr();
      this.expectPunct(')');
      return inner;
    }
    if (token.type === 'param') {
      return { kind: 'param', slot: this.parseParamSlot() };
    }
    if (token.type === 'number') {
      this.advance();
      return { kind: 'lit', value: Number(token.value) };
    }
    if (token.type === 'quoted') {
      return this.parseColumnOrFunction();
    }
    if (token.type === 'word') {
      return this.parseWordPrimary();
    }
    throw new QueryStructureError(`unexpected token: ${token.value}`);
  }

  private parseWordPrimary(): CExpr {
    const word = this.advance().value.toUpperCase();
    if (word === 'TRUE' || word === 'FALSE') {
      return { kind: 'bool', value: word === 'TRUE' };
    }
    if (word === 'NULL') {
      return { kind: 'lit', value: null };
    }
    if (word === 'EXISTS') {
      const query = this.parseParenthesizedSelect();
      return { kind: 'exists', query };
    }
    if (ALLOWED_FUNCTIONS.has(word)) {
      return this.finishFunction(word);
    }
    throw new QueryStructureError(
      `unquoted identifier or unsupported keyword: ${word}; identifiers must be double quoted`,
    );
  }

  private parseColumnOrFunction(): CExpr {
    const firstName = this.advance().value;
    if (this.peek().type === 'punct' && this.peek().value === '(') {
      const upper = firstName.toUpperCase();
      if (!ALLOWED_FUNCTIONS.has(upper)) {
        throw new QueryStructureError(`function not allowed: ${firstName}`);
      }
      return this.finishFunction(upper);
    }
    if (this.peek().type === 'punct' && this.peek().value === '.') {
      this.advance();
      const name = this.parseQuotedName();
      return { kind: 'col', qualifier: firstName, name };
    }
    return { kind: 'col', qualifier: null, name: firstName };
  }

  private finishFunction(name: string): CExpr {
    this.expectPunct('(');
    const distinct = this.consumeWord('DISTINCT');
    if (this.consumePunct('*')) {
      this.expectPunct(')');
      if (name !== 'COUNT') {
        throw new QueryStructureError(`${name}(*) is not allowed; only COUNT(*) is supported`);
      }
      return { kind: 'func', name, args: [], star: true, distinct: false };
    }
    const args: CExpr[] = [];
    if (!this.consumePunct(')')) {
      do {
        args.push(this.parseExpr());
      } while (this.consumePunct(','));
      this.expectPunct(')');
    }
    return { kind: 'func', name, args, star: false, distinct };
  }

  private parseParamSlot(): number {
    const token = this.peek();
    if (token.type !== 'param') {
      throw new QueryStructureError(`expected parameter placeholder, found: ${token.value}`);
    }
    this.advance();
    const slot = Number(token.value) - 1;
    if (!Number.isSafeInteger(slot) || slot < 0) {
      throw new QueryStructureError(`invalid parameter placeholder: $${token.value}`);
    }
    return slot;
  }
}
