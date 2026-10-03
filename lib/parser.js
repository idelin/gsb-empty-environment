'use strict';
/** Compiled equivalent of src/parser.ts (see README: offline env, no tsc). */

const KEYWORDS = new Set([
  'SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'NOT', 'EXISTS', 'IN', 'AS', 'NULL',
]);

function tokenize(sql) {
  const tokens = [];
  let i = 0;
  while (i < sql.length) {
    const ch = sql[i];
    if (/\s/.test(ch)) { i++; continue; }
    if (ch === '(') { tokens.push({ type: 'lparen', value: '(' }); i++; continue; }
    if (ch === ')') { tokens.push({ type: 'rparen', value: ')' }); i++; continue; }
    if (ch === ',') { tokens.push({ type: 'comma', value: ',' }); i++; continue; }
    if (ch === '.') { tokens.push({ type: 'dot', value: '.' }); i++; continue; }
    if (ch === '*') { tokens.push({ type: 'star', value: '*' }); i++; continue; }
    if (ch === "'") {
      let j = i + 1;
      let out = '';
      while (j < sql.length) {
        if (sql[j] === "'" && sql[j + 1] === "'") { out += "'"; j += 2; continue; }
        if (sql[j] === "'") break;
        out += sql[j];
        j++;
      }
      if (j >= sql.length) throw new Error('unterminated string literal');
      tokens.push({ type: 'string', value: out });
      i = j + 1;
      continue;
    }
    if (/[0-9]/.test(ch)) {
      let j = i;
      while (j < sql.length && /[0-9.]/.test(sql[j])) j++;
      tokens.push({ type: 'number', value: sql.slice(i, j) });
      i = j;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      let j = i;
      while (j < sql.length && /[A-Za-z0-9_]/.test(sql[j])) j++;
      tokens.push({ type: 'ident', value: sql.slice(i, j) });
      i = j;
      continue;
    }
    const two = sql.slice(i, i + 2);
    if (two === '<=' || two === '>=' || two === '<>' || two === '!=') {
      tokens.push({ type: 'op', value: two });
      i += 2;
      continue;
    }
    if (ch === '=' || ch === '<' || ch === '>') {
      tokens.push({ type: 'op', value: ch });
      i++;
      continue;
    }
    throw new Error(`unexpected character: ${ch}`);
  }
  tokens.push({ type: 'eof', value: '' });
  return tokens;
}

class Parser {
  constructor(tokens) {
    this.tokens = tokens;
    this.pos = 0;
  }

  peek() { return this.tokens[this.pos]; }

  next() { return this.tokens[this.pos++]; }

  atKeyword(kw) {
    const t = this.peek();
    return t.type === 'ident' && t.value.toUpperCase() === kw;
  }

  eatKeyword(kw) {
    if (this.atKeyword(kw)) { this.pos++; return true; }
    return false;
  }

  expectKeyword(kw) {
    if (!this.eatKeyword(kw)) {
      throw new Error(`expected keyword ${kw}, got "${this.peek().value}"`);
    }
  }

  expect(type) {
    const t = this.peek();
    if (t.type !== type) {
      throw new Error(`expected ${type}, got ${t.type} ("${t.value}")`);
    }
    return this.next();
  }

  parseSelect() {
    this.expectKeyword('SELECT');
    const select = this.parseSelectList();
    this.expectKeyword('FROM');
    const from = [this.parseTableRef()];
    while (this.peek().type === 'comma') {
      this.next();
      from.push(this.parseTableRef());
    }
    let where = null;
    if (this.eatKeyword('WHERE')) {
      where = this.parseExpr();
    }
    return { select, from, where };
  }

  parseSelectList() {
    const items = [];
    for (;;) {
      const t = this.peek();
      if (t.type === 'star') {
        this.next();
        items.push({ kind: 'star', table: null });
      } else if (
        t.type === 'ident' &&
        this.tokens[this.pos + 1].type === 'dot' &&
        this.tokens[this.pos + 2].type === 'star'
      ) {
        this.next();
        this.next();
        this.next();
        items.push({ kind: 'star', table: t.value });
      } else {
        const expr = this.parseExpr();
        let alias = null;
        if (this.eatKeyword('AS')) {
          alias = this.expect('ident').value;
        }
        items.push({ kind: 'expr', expr, alias });
      }
      if (this.peek().type === 'comma') {
        this.next();
        continue;
      }
      break;
    }
    return items;
  }

  parseTableRef() {
    const table = this.expect('ident').value;
    let alias = table;
    if (this.eatKeyword('AS')) {
      alias = this.expect('ident').value;
    } else {
      const t = this.peek();
      if (t.type === 'ident' && !KEYWORDS.has(t.value.toUpperCase())) {
        alias = this.next().value;
      }
    }
    return { table, alias };
  }

  parseExpr() { return this.parseOr(); }

  parseOr() {
    const items = [this.parseAnd()];
    while (this.eatKeyword('OR')) {
      items.push(this.parseAnd());
    }
    return items.length === 1 ? items[0] : { kind: 'or', items };
  }

  parseAnd() {
    const items = [this.parsePredicate()];
    while (this.eatKeyword('AND')) {
      items.push(this.parsePredicate());
    }
    return items.length === 1 ? items[0] : { kind: 'and', items };
  }

  parsePredicate() {
    if (this.atKeyword('NOT') && this.tokens[this.pos + 1].value.toUpperCase() === 'EXISTS') {
      this.next();
      this.next();
      return { kind: 'exists', negated: true, subquery: this.parseSubqueryParens() };
    }
    if (this.eatKeyword('EXISTS')) {
      return { kind: 'exists', negated: false, subquery: this.parseSubqueryParens() };
    }
    const left = this.parsePrimary();
    if (this.atKeyword('NOT') && this.tokens[this.pos + 1].value.toUpperCase() === 'IN') {
      this.next();
      this.next();
      return { kind: 'in', negated: true, expr: left, subquery: this.parseSubqueryParens() };
    }
    if (this.eatKeyword('IN')) {
      return { kind: 'in', negated: false, expr: left, subquery: this.parseSubqueryParens() };
    }
    if (this.peek().type === 'op') {
      const op = this.next().value;
      const right = this.parsePrimary();
      return { kind: 'binary', op, left, right };
    }
    return left;
  }

  parseSubqueryParens() {
    this.expect('lparen');
    const sub = this.parseSelect();
    this.expect('rparen');
    return sub;
  }

  parsePrimary() {
    const t = this.peek();
    if (t.type === 'lparen') {
      this.next();
      const e = this.parseExpr();
      this.expect('rparen');
      return e;
    }
    if (t.type === 'number') {
      this.next();
      return { kind: 'literal', value: Number(t.value) };
    }
    if (t.type === 'string') {
      this.next();
      return { kind: 'literal', value: t.value };
    }
    if (t.type === 'ident') {
      if (t.value.toUpperCase() === 'NULL') {
        this.next();
        return { kind: 'literal', value: null };
      }
      this.next();
      if (this.peek().type === 'dot') {
        this.next();
        const col = this.expect('ident').value;
        return { kind: 'column', table: t.value, name: col };
      }
      return { kind: 'column', table: null, name: t.value };
    }
    throw new Error(`unexpected token in expression: ${t.type} ("${t.value}")`);
  }
}

function parse(sql) {
  const parser = new Parser(tokenize(sql));
  return parser.parseSelect();
}

module.exports = { tokenize, parse };
