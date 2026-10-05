#!/usr/bin/env node
// Usage:
//   node bin/expr-edit.js format <file>
//       Print the canonically formatted expression to stdout.
//   node bin/expr-edit.js replace <file> <match-expr> <replacement-expr>
//       Replace the first node whose canonical form equals <match-expr>
//       with <replacement-expr>, keeping the node's external comments,
//       and print the result to stdout.

import { readFileSync } from 'node:fs';
import { parse } from '../src/parser.js';
import { print } from '../src/printer.js';
import { find, replaceNode, parseExpr } from '../src/edit.js';

const [, , command, ...args] = process.argv;

const canonical = (src) => print({ ...parse(src), tail: [] });

try {
  if (command === 'format') {
    process.stdout.write(print(parse(readFileSync(args[0], 'utf8'))));
  } else if (command === 'replace') {
    const [file, matchSrc, replacementSrc] = args;
    const doc = parse(readFileSync(file, 'utf8'));
    const wanted = canonical(matchSrc).trim();
    const target = find(doc.root, (n) => print({ root: n, tail: [] }).trim() === wanted);
    if (!target) {
      console.error(`no node matching: ${wanted}`);
      process.exit(1);
    }
    doc.root = replaceNode(doc.root, target, parseExpr(replacementSrc));
    process.stdout.write(print(doc));
  } else {
    console.error('usage: expr-edit format <file> | expr-edit replace <file> <match> <replacement>');
    process.exit(2);
  }
} catch (e) {
  console.error(e.message);
  process.exit(1);
}
