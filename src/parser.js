// Recursive-descent parser. Grammar (binary ops left-associative, * / bind
// tighter than + -):
//
//   expr    := term (('+' | '-') term)*
//   term    := unary (('*' | '/') unary)*
//   unary   := '-' unary | primary
//   primary := int
//            | ident
//            | ident '(' (expr (',' expr)*)? ')'
//            | '(' expr ')'
//
// Every node records `first`/`last` token indexes so comments attached to
// tokens can be re-attached to the outermost node spanning them.
// Errors report the earliest position where parsing cannot continue,
// together with the set of expected tokens.

import { lex, ParseError } from './lexer.js';

const EXPR_START = ["'-'", 'identifier', 'integer', "'('"];

class Parser {
  constructor(tokens, src) {
    this.toks = tokens;
    this.src = src;
    this.i = 0;
    this.furthest = { pos: -1, expected: new Set() };
  }

  peek() { return this.toks[this.i]; }

  isPunct(v) {
    const t = this.peek();
    return t && t.type === 'punct' && t.value === v;
  }

  endPos() {
    return this.toks.length ? this.toks[this.toks.length - 1].end : 0;
  }

  fail(pos, expected) {
    if (pos > this.furthest.pos) {
      this.furthest = { pos, expected: new Set(expected) };
    } else if (pos === this.furthest.pos) {
      for (const e of expected) this.furthest.expected.add(e);
    }
    throw new ParseError(this.furthest.pos, [...this.furthest.expected], this.src);
  }

  expectPunct(v) {
    const t = this.peek();
    if (t && t.type === 'punct' && t.value === v) { this.i++; return this.i - 1; }
    this.fail(t ? t.start : this.endPos(), [`'${v}'`]);
  }

  parseExpr() {
    let left = this.parseTerm();
    for (;;) {
      if (this.isPunct('+') || this.isPunct('-')) {
        const op = this.peek().value;
        this.i++;
        const right = this.parseTerm();
        left = { kind: 'binary', op, left, right, first: left.first, last: right.last };
      } else {
        return left;
      }
    }
  }

  parseTerm() {
    let left = this.parseUnary();
    for (;;) {
      if (this.isPunct('*') || this.isPunct('/')) {
        const op = this.peek().value;
        this.i++;
        const right = this.parseUnary();
        left = { kind: 'binary', op, left, right, first: left.first, last: right.last };
      } else {
        return left;
      }
    }
  }

  parseUnary() {
    const t = this.peek();
    const idx = this.i;
    if (t && t.type === 'punct' && t.value === '-') {
      this.i++;
      const arg = this.parseUnary();
      return { kind: 'unary', op: '-', arg, first: idx, last: arg.last };
    }
    if (t && (t.type === 'int' || t.type === 'ident' || (t.type === 'punct' && t.value === '('))) {
      return this.parsePrimary();
    }
    this.fail(t ? t.start : this.endPos(), EXPR_START);
  }

  parsePrimary() {
    const t = this.peek();
    const idx = this.i;
    if (t.type === 'int') {
      this.i++;
      return { kind: 'num', value: Number(t.value), raw: t.value, first: idx, last: idx };
    }
    if (t.type === 'ident') {
      this.i++;
      if (this.isPunct('(')) {
        this.i++;
        const args = [];
        if (!this.isPunct(')')) {
          args.push(this.parseExpr());
          while (this.isPunct(',')) {
            this.i++;
            args.push(this.parseExpr());
          }
        }
        const close = this.expectPunct(')');
        return { kind: 'call', name: t.value, args, first: idx, last: close };
      }
      return { kind: 'ident', name: t.value, first: idx, last: idx };
    }
    // t is '('
    this.i++;
    const inner = this.parseExpr();
    const close = this.expectPunct(')');
    return { kind: 'paren', expr: inner, first: idx, last: close };
  }

  parseProgram() {
    if (!this.toks.length) this.fail(0, EXPR_START);
    const root = this.parseExpr();
    const t = this.peek();
    if (t) this.fail(t.start, ["'+'", "'-'", "'*'", "'/'", "')'", "','", 'end of input']);
    return root;
  }
}

// Attach token comments to AST nodes.
//   - a token's leading comments go to the outermost node starting at the
//     nearest node-starting token at or after it;
//   - a token's trailing comment goes to the outermost node ending at the
//     nearest node-ending token at or before it (so a comment after a
//     comma or operator belongs to the preceding operand); if no node ends
//     before it, it becomes a leading comment of the next node instead.
function attachComments(root, tokens) {
  const byFirst = new Map();
  const byLast = new Map();
  const visit = (node, parent) => {
    node.leading = [];
    node.trailing = null;
    if (!parent || parent.first !== node.first) byFirst.set(node.first, node);
    if (!parent || parent.last !== node.last) byLast.set(node.last, node);
    for (const child of children(node)) visit(child, node);
  };
  visit(root, null);
  const firsts = [...byFirst.keys()].sort((a, b) => a - b);
  const lasts = [...byLast.keys()].sort((a, b) => a - b);
  const firstGe = (k) => firsts.find((f) => f >= k);
  const lastLe = (k) => {
    for (let i = lasts.length - 1; i >= 0; i--) if (lasts[i] <= k) return lasts[i];
    return undefined;
  };
  tokens.forEach((tok, k) => {
    for (const c of tok.leading) byFirst.get(firstGe(k)).leading.push(c);
    if (tok.trailing) {
      const l = lastLe(k);
      if (l !== undefined) byLast.get(l).trailing = tok.trailing;
      else byFirst.get(firstGe(k)).leading.push(tok.trailing);
    }
  });
}

export function children(node) {
  switch (node.kind) {
    case 'unary': return [node.arg];
    case 'binary': return [node.left, node.right];
    case 'call': return node.args;
    case 'paren': return [node.expr];
    default: return [];
  }
}

// parse(src) -> { root, tail } where tail holds end-of-file comments.
export function parse(src) {
  const { tokens, tail } = lex(src);
  const parser = new Parser(tokens, src);
  const root = parser.parseProgram();
  attachComments(root, tokens);
  return { root, tail };
}
