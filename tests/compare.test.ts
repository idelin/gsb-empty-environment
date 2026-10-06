// Differential tests: every program runs through the reference AST
// interpreter and through the boxing transform + machine, and both must
// produce identical traces and results. Structural assertions on the
// transformed AST pin down the boxing decisions.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  bin, bool, call, expr, func, if_, let_, nil, num, print, ret, set, v, while_,
} from '../src/ast.ts';
import type { Program, TStmt } from '../src/ast.ts';
import { runProgram } from '../src/interp.ts';
import { transform } from '../src/transform.ts';
import { runTransformed } from '../src/machine.ts';
import type { MachineStats } from '../src/machine.ts';

function runBoth(prog: Program): { tprog: TStmt[]; stats: MachineStats; trace: string[] } {
  const traceRef: string[] = [];
  const traceBoxed: string[] = [];
  const resultRef = runProgram(prog, traceRef);
  const tprog = transform(prog);
  const { result: resultBoxed, stats } = runTransformed(tprog, traceBoxed);
  assert.deepEqual(traceBoxed, traceRef, 'traces must match');
  assert.deepEqual(resultBoxed, resultRef, 'results must match');
  return { tprog, stats, trace: traceRef };
}

type TFunc = Extract<TStmt, { tag: 'func' }>;

function findFunc(stmts: TStmt[], name: string): TFunc {
  for (const stmt of stmts) {
    if (stmt.tag === 'func') {
      if (stmt.name === name) return stmt;
      const hit = findFunc(stmt.body, name);
      if (hit) return hit;
    } else if (stmt.tag === 'if') {
      const hit = findFunc(stmt.then, name) ?? findFunc(stmt.else, name);
      if (hit) return hit;
    } else if (stmt.tag === 'while') {
      const hit = findFunc(stmt.body, name);
      if (hit) return hit;
    }
  }
  throw new Error(`function not found: ${name}`);
}

function letBoxedFlags(stmts: TStmt[], acc: Map<string, boolean> = new Map()): Map<string, boolean> {
  for (const stmt of stmts) {
    if (stmt.tag === 'let') acc.set(stmt.name, stmt.boxed);
    else if (stmt.tag === 'func') letBoxedFlags(stmt.body, acc);
    else if (stmt.tag === 'if') {
      letBoxedFlags(stmt.then, acc);
      letBoxedFlags(stmt.else, acc);
    } else if (stmt.tag === 'while') letBoxedFlags(stmt.body, acc);
  }
  return acc;
}

test('sibling closures share one box; separate calls get separate boxes', () => {
  const prog: Program = [
    func('makeCounter', [],
      let_('count', num(0)),
      let_('scratch', num(100)), // mutated but never captured: stays on stack
      set('scratch', bin('+', v('scratch'), num(1))),
      func('inc', [], set('count', bin('+', v('count'), num(1))), ret(v('count'))),
      func('get', [], ret(v('count'))),
      print(call(v('get'))),
      print(call(v('inc'))),
      print(call(v('inc'))),
      print(call(v('get'))),
      ret(v('inc')),
    ),
    let_('a', call(v('makeCounter'))),
    let_('b', call(v('makeCounter'))),
    print(call(v('a'))),
    print(call(v('b'))),
  ];
  const { tprog, stats, trace } = runBoth(prog);
  assert.deepEqual(trace, ['0', '1', '2', '2', '0', '1', '2', '2', '3', '3']);
  assert.equal(stats.boxesAllocated, 2); // one `count` box per makeCounter call
  const flags = letBoxedFlags(tprog);
  assert.equal(flags.get('count'), true);
  assert.equal(flags.get('scratch'), false);
});

test('closures created in a loop: per-iteration boxes, shared outer variable', () => {
  const prog: Program = [
    let_('i', num(0)),
    let_('f0', nil),
    let_('f1', nil),
    let_('f2', nil),
    while_(bin('<', v('i'), num(3)),
      let_('j', bin('*', v('i'), num(10))),
      func('bump', [], set('j', bin('+', v('j'), num(1))), ret(bin('+', v('j'), v('i')))),
      if_(bin('==', v('i'), num(0)), [set('f0', v('bump'))], [
        if_(bin('==', v('i'), num(1)), [set('f1', v('bump'))], [set('f2', v('bump'))]),
      ]),
      set('i', bin('+', v('i'), num(1))),
    ),
    print(call(v('f0'))), // j: 0 -> 1, shared i = 3  => 4
    print(call(v('f1'))), // 11 + 3 => 14
    print(call(v('f2'))), // 21 + 3 => 24
    print(call(v('f0'))), // 2 + 3 => 5 (f0 kept its own j box)
  ];
  const { tprog, stats, trace } = runBoth(prog);
  assert.deepEqual(trace, ['4', '14', '24', '5']);
  assert.equal(stats.boxesAllocated, 4); // 1 shared box for i + 3 per-iteration boxes for j
  const flags = letBoxedFlags(tprog);
  assert.equal(flags.get('i'), true);
  assert.equal(flags.get('j'), true);
  assert.equal(flags.get('f0'), false);
  const bump = findFunc(tprog, 'bump');
  assert.deepEqual(bump.captures, [
    { name: 'i', by: 'box' },
    { name: 'j', by: 'box' },
  ]);
});

test('recursion: nested recursive helper mutating a captured accumulator', () => {
  const prog: Program = [
    func('fact', ['n'],
      let_('acc', num(1)),
      func('go', ['k'],
        if_(bin('>', v('k'), num(1)), [
          set('acc', bin('*', v('acc'), v('k'))),
          expr(call(v('go'), bin('-', v('k'), num(1)))),
        ]),
      ),
      expr(call(v('go'), v('n'))),
      ret(v('acc')),
    ),
    print(call(v('fact'), num(5))),
    print(call(v('fact'), num(0))),
  ];
  const { tprog, stats, trace } = runBoth(prog);
  assert.deepEqual(trace, ['120', '1']);
  assert.equal(stats.boxesAllocated, 2); // one `acc` box per fact call
  const go = findFunc(tprog, 'go');
  assert.equal(go.selfRef, true);
  assert.deepEqual(go.captures, [{ name: 'acc', by: 'box' }]);
});

test('recursion: each call instance gets its own box, closures escape mid-recursion', () => {
  const prog: Program = [
    func('build', ['n'],
      let_('val', bin('*', v('n'), v('n'))),
      func('g', [], set('val', bin('+', v('val'), num(1))), ret(v('val'))),
      if_(bin('==', v('n'), num(0)), [ret(v('g'))]),
      let_('inner', call(v('build'), bin('-', v('n'), num(1)))),
      print(call(v('g'))),
      ret(v('inner')),
    ),
    let_('h', call(v('build'), num(3))),
    print(call(v('h'))),
    print(call(v('h'))),
  ];
  const { stats, trace } = runBoth(prog);
  // build(1) g: 1->2, build(2) g: 4->5, build(3) g: 9->10, then h = build(0)'s g: 0->1->2
  assert.deepEqual(trace, ['2', '5', '10', '1', '2']);
  assert.equal(stats.boxesAllocated, 4); // one `val` box per build call
});

test('immutable captures are by value; pass-through functions forward captures', () => {
  const prog: Program = [
    let_('scale', num(7)), // captured, never mutated
    func('outer', [],
      let_('factor', num(3)),
      func('middle', [],
        func('inner', [], set('factor', bin('*', v('factor'), num(2))), ret(bin('*', v('factor'), v('scale')))),
        ret(v('inner')),
      ),
      let_('f', call(v('middle'))),
      print(call(v('f'))),
      print(call(v('f'))),
      ret(v('factor')),
    ),
    print(call(v('outer'))),
  ];
  const { tprog, stats, trace } = runBoth(prog);
  assert.deepEqual(trace, ['42', '84', '12']);
  assert.equal(stats.boxesAllocated, 1); // only `factor`
  assert.deepEqual(findFunc(tprog, 'outer').captures, [{ name: 'scale', by: 'value' }]);
  assert.deepEqual(findFunc(tprog, 'middle').captures, [
    { name: 'factor', by: 'box' },
    { name: 'scale', by: 'value' },
  ]);
  assert.deepEqual(findFunc(tprog, 'inner').captures, [
    { name: 'factor', by: 'box' },
    { name: 'scale', by: 'value' },
  ]);
});

test('uncaptured mutated locals stay on the stack (zero boxes)', () => {
  const prog: Program = [
    func('sumTo', ['n'],
      let_('total', num(0)),
      let_('k', v('n')),
      while_(bin('>', v('k'), num(0)),
        set('total', bin('+', v('total'), v('k'))),
        set('k', bin('-', v('k'), num(1))),
      ),
      ret(v('total')),
    ),
    print(call(v('sumTo'), num(100))),
    ret(call(v('sumTo'), num(10))),
  ];
  const { tprog, stats, trace } = runBoth(prog);
  assert.deepEqual(trace, ['5050']);
  assert.equal(stats.boxesAllocated, 0);
  const flags = letBoxedFlags(tprog);
  assert.equal(flags.get('total'), false);
  assert.equal(flags.get('k'), false);
  assert.deepEqual(findFunc(tprog, 'sumTo').boxedParams, []);
});

test('captured mutable parameters are boxed', () => {
  const prog: Program = [
    func('make', ['step'],
      func('next', [], set('step', bin('+', v('step'), num(1))), ret(v('step'))),
      ret(v('next')),
    ),
    let_('n1', call(v('make'), num(10))),
    let_('n2', call(v('make'), num(100))),
    print(call(v('n1'))),
    print(call(v('n1'))),
    print(call(v('n2'))),
    print(bool(true)), // trace formatting sanity for booleans
  ];
  const { tprog, stats, trace } = runBoth(prog);
  assert.deepEqual(trace, ['11', '12', '101', 'true']);
  assert.equal(stats.boxesAllocated, 2); // one boxed `step` per make call
  assert.deepEqual(findFunc(tprog, 'make').boxedParams, ['step']);
});
