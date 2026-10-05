// Printer / formatter. Rules:
//   - whitespace is normalized (single spaces around binary operators,
//     no space after unary '-', `f(a, b)` for calls);
//   - leading comments are printed on their own lines before their node;
//   - a trailing comment is printed on the same line right after its node,
//     followed by a line break;
//   - end-of-file tail comments are printed at the end, one per line.
// Comments, AST structure and token values are preserved; tokens are never
// merged (operators are always separated from operands).

import { parse } from './parser.js';

class Printer {
  constructor() { this.buf = ''; }

  atLineStart() { return this.buf.length === 0 || this.buf.endsWith('\n'); }

  write(s) { this.buf += s; }

  nl() {
    this.buf = this.buf.replace(/[ \t]+$/, '');
    if (!this.buf.endsWith('\n')) this.buf += '\n';
  }

  node(node) {
    for (const c of node.leading) {
      if (!this.atLineStart()) this.nl();
      this.write(c.text);
      this.nl();
    }
    this.expr(node);
    if (node.trailing) {
      this.write(' ');
      this.write(node.trailing.text);
      this.nl();
    }
  }

  expr(node) {
    switch (node.kind) {
      case 'num':
        this.write(node.raw);
        break;
      case 'ident':
        this.write(node.name);
        break;
      case 'unary':
        this.write('-');
        this.node(node.arg);
        break;
      case 'binary':
        this.node(node.left);
        this.write(' ' + node.op + ' ');
        this.node(node.right);
        break;
      case 'call':
        this.write(node.name + '(');
        node.args.forEach((arg, i) => {
          if (i > 0) this.write(', ');
          this.node(arg);
        });
        this.write(')');
        break;
      case 'paren':
        this.write('(');
        this.node(node.expr);
        this.write(')');
        break;
      default:
        throw new Error(`unknown node kind: ${node.kind}`);
    }
  }
}

// print(doc) -> canonical text for a parsed document { root, tail }.
export function print(doc) {
  const p = new Printer();
  p.node(doc.root);
  for (const c of doc.tail) {
    if (!p.atLineStart()) p.nl();
    p.write(c.text);
    p.nl();
  }
  let out = p.buf.replace(/[ \t]+\n/g, '\n');
  out = out.replace(/\n*$/, '');
  return out + '\n';
}

// format(src) -> canonical text; satisfies format(format(src)) === format(src).
export function format(src) {
  return print(parse(src));
}
