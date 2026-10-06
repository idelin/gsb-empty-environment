/**
 * Recursive-descent parser for the SQL dialect emitted by render().
 * Used to re-parse rendered SQL so bindings can be checked against the
 * original AST. Placeholders become numeric references ($n -> index n).
 */

export interface PField {
  kind: 'field';
  path: string[];
}

export interface PRaw {
  kind: 'raw';
  text: string;
}

export type POperand = PField | PRaw;

export type PFrom =
  | { kind: 'table'; path: string[] }
  | { kind: 'subquery'; query: PQuery; alias: string };

export type PCondition =
  | { kind: 'cmp'; left: POperand; op: string; placeholder: number }
  | { kind: 'inList'; left: POperand; negated: boolean; placeholders: number[] }
  | { kind: 'inSub'; left: POperand; negated: boolean; query: PQuery }
  | { kind: 'and'; children: PCondition[] }
  | { kind: 'or'; children: PCondition[] }
  | { kind: 'const'; value: boolean };

export interface POrderItem {
  expr: POperand;
  dir: string;
}

export interface PQuery {
  kind: 'query';
  select: POperand[];
  from: PFrom;
  where: PCondition | undefined;
  orderBy: POrderItem[];
  limit: number | undefined;
}

type Tok =
  | { t: 'ident'; v: string }
  | { t: 'ph'; v: number }
  | { t: 'num'; v: string }
  | { t: 'word'; v: string }
  | { t: 'punct'; v: string };

function tokenize(sql: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < sql.length) {
    const ch = sql[i];
    if (/\s/.test(ch)) {
      i++;
      continue;
    }
    if (ch === '"') {
      let j = i + 1;
      let name = '';
      for (;;) {
        if (j >= sql.length) throw new Error('unterminated quoted identifier');
        if (sql[j] === '"') {
          if (sql[j + 1] === '"') {
            name += '"';
            j += 2;
            continue;
          }
          j++;
          break;
        }
        name += sql[j];
        j++;
      }
      out.push({ t: 'ident', v: name });
      i = j;
      continue;
    }
    if (ch === '$') {
      const m = /^\$(\d+)/.exec(sql.slice(i));
      if (!m) throw new Error(`bad placeholder at offset ${i}`);
      out.push({ t: 'ph', v: Number(m[1]) });
      i += m[0].length;
      continue;
    }
    if (/[0-9]/.test(ch)) {
      const m = /^\d+/.exec(sql.slice(i));
      out.push({ t: 'num', v: m![0] });
      i += m![0].length;
      continue;
    }
    const two = sql.slice(i, i + 2);
    if (two === '<=' || two === '>=' || two === '<>' || two === '!=') {
      out.push({ t: 'punct', v: two });
      i += 2;
      continue;
    }
    if ('(),.=<>*'.includes(ch)) {
      out.push({ t: 'punct', v: ch });
      i++;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(sql.slice(i));
      out.push({ t: 'word', v: m![0] });
      i += m![0].length;
      continue;
    }
    throw new Error(`tokenizer: unexpected character ${JSON.stringify(ch)} at offset ${i}`);
  }
  return out;
}

function tokText(tok: Tok): string {
  switch (tok.t) {
    case 'ident':
      return '"' + tok.v.replace(/"/g, '""') + '"';
    case 'ph':
      return '$' + tok.v;
    default:
      return tok.v;
  }
}

function joinRaw(toks: Tok[]): string {
  let out = '';
  for (const tok of toks) {
    const text = tokText(tok);
    if (out === '') {
      out = text;
    } else if (text === ')' || text === ',' || text === '.') {
      out += text;
    } else if (text === '(' && /[A-Za-z0-9_]$/.test(out)) {
      out += text;
    } else if (out.endsWith('(') || out.endsWith('.')) {
      out += text;
    } else {
      out += ' ' + text;
    }
  }
  return out;
}

const OPERAND_STOP_WORDS = new Set([
  'AND',
  'OR',
  'NOT',
  'IN',
  'LIKE',
  'FROM',
  'WHERE',
  'ORDER',
  'LIMIT',
  'ASC',
  'DESC',
]);
const OPERAND_STOP_PUNCTS = new Set(['=', '<', '>', '<=', '>=', '<>', '!=', ',']);
const COMPARISON_OPS = new Set(['=', '<>', '!=', '<', '<=', '>', '>=']);

class Parser {
  private pos = 0;
  private readonly toks: Tok[];

  constructor(toks: Tok[]) {
    this.toks = toks;
  }

  private peekAt(offset: number): Tok | undefined {
    return this.toks[this.pos + offset];
  }

  private peek(): Tok | undefined {
    return this.toks[this.pos];
  }

  private next(): Tok {
    const tok = this.toks[this.pos];
    if (!tok) throw new Error('unexpected end of SQL');
    this.pos++;
    return tok;
  }

  private atWord(word: string): boolean {
    const tok = this.peek();
    return !!tok && tok.t === 'word' && tok.v.toUpperCase() === word;
  }

  private eatWord(word: string): boolean {
    if (this.atWord(word)) {
      this.pos++;
      return true;
    }
    return false;
  }

  private expectWord(word: string): void {
    if (!this.eatWord(word)) {
      throw new Error(`expected ${word} at token ${this.pos}`);
    }
  }

  private atPunct(punct: string): boolean {
    const tok = this.peek();
    return !!tok && tok.t === 'punct' && tok.v === punct;
  }

  private eatPunct(punct: string): boolean {
    if (this.atPunct(punct)) {
      this.pos++;
      return true;
    }
    return false;
  }

  private expectPunct(punct: string): void {
    if (!this.eatPunct(punct)) {
      throw new Error(`expected '${punct}' at token ${this.pos}`);
    }
  }

  private nextIdent(): string {
    const tok = this.next();
    if (tok.t !== 'ident') throw new Error(`expected identifier at token ${this.pos - 1}`);
    return tok.v;
  }

  private nextPlaceholder(): number {
    const tok = this.next();
    if (tok.t !== 'ph') throw new Error(`expected placeholder at token ${this.pos - 1}`);
    return tok.v;
  }

  private expectEnd(): void {
    if (this.pos !== this.toks.length) {
      throw new Error(`unexpected trailing tokens at token ${this.pos}`);
    }
  }

  private parseIdentPath(): string[] {
    const path = [this.nextIdent()];
    while (this.eatPunct('.')) {
      path.push(this.nextIdent());
    }
    return path;
  }

  private collectRaw(stopWords: Set<string>, stopPuncts: Set<string>): Tok[] {
    const out: Tok[] = [];
    let depth = 0;
    for (;;) {
      const tok = this.peek();
      if (!tok) break;
      if (tok.t === 'punct') {
        if (tok.v === '(') {
          depth++;
          out.push(this.next());
          continue;
        }
        if (tok.v === ')') {
          if (depth === 0) break;
          depth--;
          out.push(this.next());
          continue;
        }
        if (depth === 0 && stopPuncts.has(tok.v)) break;
        out.push(this.next());
        continue;
      }
      if (tok.t === 'word' && depth === 0 && stopWords.has(tok.v.toUpperCase())) break;
      if (tok.t === 'ph') throw new Error('placeholder inside raw fragment');
      out.push(this.next());
    }
    return out;
  }

  private parseOperand(): POperand {
    const tok = this.peek();
    if (tok && tok.t === 'ident') {
      return { kind: 'field', path: this.parseIdentPath() };
    }
    const toks = this.collectRaw(OPERAND_STOP_WORDS, OPERAND_STOP_PUNCTS);
    if (toks.length === 0) throw new Error(`expected operand at token ${this.pos}`);
    return { kind: 'raw', text: joinRaw(toks) };
  }

  private parseSelectItem(): POperand {
    const tok = this.peek();
    if (tok && tok.t === 'ident') {
      return { kind: 'field', path: this.parseIdentPath() };
    }
    const toks = this.collectRaw(new Set(['FROM']), new Set([',']));
    if (toks.length === 0) throw new Error(`expected select item at token ${this.pos}`);
    return { kind: 'raw', text: joinRaw(toks) };
  }

  private isConstMarker(): boolean {
    const a = this.peekAt(0);
    const b = this.peekAt(1);
    const c = this.peekAt(2);
    const d = this.peekAt(3);
    const e = this.peekAt(4);
    return (
      !!a && a.t === 'punct' && a.v === '(' &&
      !!b && b.t === 'num' &&
      !!c && c.t === 'punct' && c.v === '=' &&
      !!d && d.t === 'num' &&
      !!e && e.t === 'punct' && e.v === ')'
    );
  }

  private parseCondition(): PCondition {
    const first = this.parseAndLevel();
    if (!this.atWord('OR')) return first;
    const children = [first];
    while (this.eatWord('OR')) {
      children.push(this.parseAndLevel());
    }
    return { kind: 'or', children };
  }

  private parseAndLevel(): PCondition {
    const first = this.parsePrimary();
    if (!this.atWord('AND')) return first;
    const children = [first];
    while (this.eatWord('AND')) {
      children.push(this.parsePrimary());
    }
    return { kind: 'and', children };
  }

  private parsePrimary(): PCondition {
    if (this.atPunct('(')) {
      if (this.isConstMarker()) {
        this.next();
        const left = this.next();
        this.next();
        const right = this.next();
        this.next();
        return { kind: 'const', value: left.t === 'num' && right.t === 'num' && left.v === right.v };
      }
      this.next();
      const inner = this.parseCondition();
      this.expectPunct(')');
      return inner;
    }
    return this.parsePredicate();
  }

  private parsePredicate(): PCondition {
    const left = this.parseOperand();
    if (this.eatWord('NOT')) {
      this.expectWord('IN');
      return this.parseIn(left, true);
    }
    if (this.eatWord('IN')) {
      return this.parseIn(left, false);
    }
    let op: string;
    if (this.eatWord('LIKE')) {
      op = 'LIKE';
    } else {
      const tok = this.next();
      if (tok.t !== 'punct' || !COMPARISON_OPS.has(tok.v)) {
        throw new Error(`expected comparison operator at token ${this.pos - 1}`);
      }
      op = tok.v;
    }
    return { kind: 'cmp', left, op, placeholder: this.nextPlaceholder() };
  }

  private parseIn(left: POperand, negated: boolean): PCondition {
    this.expectPunct('(');
    if (this.atWord('SELECT')) {
      const query = this.parseQuery();
      this.expectPunct(')');
      return { kind: 'inSub', left, negated, query };
    }
    const placeholders: number[] = [];
    while (this.peek()?.t === 'ph') {
      placeholders.push(this.nextPlaceholder());
      this.eatPunct(',');
    }
    this.expectPunct(')');
    return { kind: 'inList', left, negated, placeholders };
  }

  parseQuery(): PQuery {
    this.expectWord('SELECT');
    const selectItems: POperand[] = [];
    for (;;) {
      selectItems.push(this.parseSelectItem());
      if (!this.eatPunct(',')) break;
    }
    this.expectWord('FROM');
    let from: PFrom;
    if (this.eatPunct('(')) {
      const subquery = this.parseQuery();
      this.expectPunct(')');
      this.expectWord('AS');
      from = { kind: 'subquery', query: subquery, alias: this.nextIdent() };
    } else {
      from = { kind: 'table', path: this.parseIdentPath() };
    }
    let where: PCondition | undefined;
    if (this.eatWord('WHERE')) {
      where = this.parseCondition();
    }
    const orderBy: POrderItem[] = [];
    if (this.eatWord('ORDER')) {
      this.expectWord('BY');
      for (;;) {
        const expr = this.parseOperand();
        let dir = 'ASC';
        if (this.eatWord('DESC')) dir = 'DESC';
        else this.eatWord('ASC');
        orderBy.push({ expr, dir });
        if (!this.eatPunct(',')) break;
      }
    }
    let limit: number | undefined;
    if (this.eatWord('LIMIT')) {
      limit = this.nextPlaceholder();
    }
    return { kind: 'query', select: selectItems, from, where, orderBy, limit };
  }
}

export function parse(sql: string): PQuery {
  const parser = new Parser(tokenize(sql));
  const query = parser.parseQuery();
  parser.expectEnd();
  return query;
}
