// Demo: build a deeply nested expression AST, pretty-print it within 80
// columns, re-parse the output, and verify the AST is structurally identical.

import assert from 'node:assert/strict';
import { parse } from '../src/parser.ts';
import { prettyPrint } from '../src/printer.ts';
import type { Expr, Prop } from '../src/ast.ts';

const num = (value: number): Expr => ({ kind: 'num', value });
const str = (value: string): Expr => ({ kind: 'str', value });
const id = (name: string): Expr => ({ kind: 'ident', name });
const bin = (op: string, left: Expr, right: Expr): Expr => ({ kind: 'binary', op, left, right });
const un = (op: string, argument: Expr): Expr => ({ kind: 'unary', op, argument });
const cond = (testE: Expr, consequent: Expr, alternate: Expr): Expr => ({
  kind: 'cond',
  test: testE,
  consequent,
  alternate,
});
const call = (callee: Expr, ...args: Expr[]): Expr => ({ kind: 'call', callee, args });
const mem = (object: Expr, property: string): Expr => ({ kind: 'member', object, property });
const idx = (object: Expr, index: Expr): Expr => ({ kind: 'index', object, index });
const arr = (...elements: Expr[]): Expr => ({ kind: 'array', elements });
const obj = (...props: Prop[]): Expr => ({ kind: 'object', props });
const prop = (key: string, value: Expr): Prop => ({ key, value });

const ast: Expr = cond(
  bin(
    '&&',
    bin('>=', mem(id('response'), 'status'), num(200)),
    bin('<', mem(id('response'), 'status'), num(300)),
  ),
  call(
    mem(id('transformer'), 'normalize'),
    obj(
      prop(
        'user',
        obj(
          prop('id', mem(mem(id('response'), 'payload'), 'userId')),
          prop('displayName', bin('||', mem(mem(id('response'), 'payload'), 'name'), str('anonymous'))),
          prop('tags', arr(id('alpha'), id('beta'), bin('+', str('tag-'), mem(id('response'), 'kind')))),
        ),
      ),
      prop(
        'metrics',
        obj(
          prop('score', bin('+', bin('*', id('weight'), mem(id('response'), 'rawScore')), id('bias'))),
          prop(
            'history',
            call(
              mem(mem(id('response'), 'history'), 'slice'),
              num(0),
              bin('-', mem(mem(id('response'), 'history'), 'length'), num(1)),
            ),
          ),
        ),
      ),
    ),
    idx(id('fallbacks'), bin('%', id('attemptIndex'), mem(id('fallbacks'), 'length'))),
  ),
  call(
    mem(id('logger'), 'error'),
    str('request failed'),
    cond(un('!', id('retriable')), num(-1), bin('+', id('retryCount'), num(1))),
  ),
);

const printed = prettyPrint(ast, 80);
console.log('--- pretty printed (80 cols) ---');
console.log(printed);
console.log('--------------------------------');

const lines = printed.split('\n');
const maxLen = Math.max(...lines.map((l) => l.length));
console.log(`lines: ${lines.length}, longest line: ${maxLen} columns`);

const reparsed = parse(printed);
assert.deepStrictEqual(reparsed, ast);
assert.strictEqual(prettyPrint(reparsed, 80), printed);
console.log('round trip OK: re-parsed AST is structurally identical, printing is idempotent');
