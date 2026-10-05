'use strict';

const { tokenize, ParseError } = require('./lexer');

// Grammar (binary operators are left-associative, * and / bind tighter):
//   expr    := term (('+' | '-') term)*
//   term    := factor (('*' | '/') factor)*
//   factor  := '-' factor | primary
//   primary := IDENT '(' args? ')' | IDENT | INT | '(' expr ')'
//   args    := expr (',' expr)*
//
// AST nodes: { kind: 'num', value } | { kind: 'ident', name }
//          | { kind: 'neg', expr } | { kind: 'call', name, args }
//          | { kind: 'bin', op, left, right }
// Every node records tok0/tok1, the inclusive indices of its first and
// last tokens in the token stream, so printing and splicing can map the
// node back to its source span (and its surrounding comment trivia).

class Parser {
  constructor(tokens) {
    this.tokens = tokens;
    this.pos = 0;
  }

  peek(offset = 0) {
    return this.tokens[Math.min(this.pos + offset, this.tokens.length - 1)];
  }

  advance() {
    return this.tokens[this.pos++];
  }

  error(expected) {
    throw new ParseError(this.peek().start, expected);
  }

  expect(type, value) {
    const tok = this.peek();
    if (tok.type !== type || (value !== undefined && tok.text !== value)) {
      this.error([value === undefined ? type : `'${value}'`]);
    }
    return this.advance();
  }

  parseExpr() {
    let left = this.parseTerm();
    while (this.peek().type === 'op' && (this.peek().text === '+' || this.peek().text === '-')) {
      const op = this.advance().text;
      const right = this.parseTerm();
      left = { kind: 'bin', op, left, right, tok0: left.tok0, tok1: right.tok1 };
    }
    return left;
  }

  parseTerm() {
    let left = this.parseFactor();
    while (this.peek().type === 'op' && (this.peek().text === '*' || this.peek().text === '/')) {
      const op = this.advance().text;
      const right = this.parseFactor();
      left = { kind: 'bin', op, left, right, tok0: left.tok0, tok1: right.tok1 };
    }
    return left;
  }

  parseFactor() {
    const tok = this.peek();
    if (tok.type === 'op' && tok.text === '-') {
      this.advance();
      const expr = this.parseFactor();
      return { kind: 'neg', expr, tok0: this.pos - 1, tok1: expr.tok1 };
    }
    return this.parsePrimary();
  }

  parsePrimary() {
    const tok = this.peek();
    const tok0 = this.pos;
    if (tok.type === 'int') {
      this.advance();
      return { kind: 'num', value: parseInt(tok.text, 10), tok0, tok1: tok0 };
    }
    if (tok.type === 'ident') {
      this.advance();
      if (this.peek().type === 'lparen') {
        this.advance();
        const args = [];
        if (this.peek().type !== 'rparen') {
          args.push(this.parseExpr());
          while (this.peek().type === 'comma') {
            this.advance();
            args.push(this.parseExpr());
          }
        }
        this.expect('rparen', ')');
        return { kind: 'call', name: tok.text, args, tok0, tok1: this.pos - 1 };
      }
      return { kind: 'ident', name: tok.text, tok0, tok1: tok0 };
    }
    if (tok.type === 'lparen') {
      this.advance();
      const inner = this.parseExpr();
      this.expect('rparen', ')');
      return { ...inner, tok0, tok1: this.pos - 1 };
    }
    this.error(['identifier', 'integer', `'('`]);
  }
}

function parse(src) {
  const tokens = tokenize(src);
  const parser = new Parser(tokens);
  const ast = parser.parseExpr();
  if (parser.peek().type !== 'eof') {
    throw new ParseError(parser.peek().start, ['end of input', `'+'`, `'-'`, `'*'`, `'/'`, `')'`, `','`]);
  }
  return { ast, tokens, src };
}

// Structural equality, ignoring source-span bookkeeping.
function astEqual(a, b) {
  if (a.kind !== b.kind) return false;
  switch (a.kind) {
    case 'num':
      return a.value === b.value;
    case 'ident':
      return a.name === b.name;
    case 'neg':
      return astEqual(a.expr, b.expr);
    case 'call':
      return a.name === b.name && a.args.length === b.args.length &&
        a.args.every((arg, i) => astEqual(arg, b.args[i]));
    case 'bin':
      return a.op === b.op && astEqual(a.left, b.left) && astEqual(a.right, b.right);
    default:
      return false;
  }
}

// Depth-first search for the first node matching pred.
function findNode(node, pred) {
  if (pred(node)) return node;
  const children =
    node.kind === 'neg' ? [node.expr] :
    node.kind === 'call' ? node.args :
    node.kind === 'bin' ? [node.left, node.right] : [];
  for (const child of children) {
    const hit = findNode(child, pred);
    if (hit) return hit;
  }
  return null;
}

module.exports = { parse, astEqual, findNode, ParseError };
