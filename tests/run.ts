// Comparison suite: every program runs through the reference interpreter
// (original AST) and through the compiler + runtime (boxing transform), and
// both must agree on the return value and the printed output. Runtime-only
// invariants (box identity, stack discipline) are asserted on the executor.

import { Program } from '../src/ast';
import { run, InterpResult } from '../src/interp';
import { compile } from '../src/compiler/transform';
import {
  execute,
  isClosure,
  upvalBox,
  ExecResult,
  Value,
  TClosure,
} from '../src/runtime/executor';
import { normalize } from '../src/semantics';
import * as b from '../src/builder';

declare const process: { exit(code: number): void };

let passed = 0;
let failed = 0;

function check(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.log(`FAIL ${name}`);
    console.log(`     ${(err as Error).message}`);
  }
}

interface Both {
  interp: InterpResult;
  exec: ExecResult;
}

function runBoth(program: Program): Both {
  const interp = run(program);
  const exec = execute(compile(program));
  const got = JSON.stringify(normalize(exec.value));
  const want = JSON.stringify(normalize(interp.value));
  check(got === want, `result mismatch: compiled=${got} interpreted=${want}`);
  check(
    JSON.stringify(exec.output) === JSON.stringify(interp.output),
    `output mismatch: compiled=${JSON.stringify(exec.output)} interpreted=${JSON.stringify(interp.output)}`,
  );
  check(
    exec.stats.framesPushed === exec.stats.framesPopped,
    `call stack not balanced: pushed=${exec.stats.framesPushed} popped=${exec.stats.framesPopped}`,
  );
  return { interp, exec };
}

function asClosureArray(v: Value | null): TClosure[] {
  check(Array.isArray(v), 'expected an array of closures');
  for (const item of v as Value[]) check(isClosure(item), 'expected a closure');
  return v as TClosure[];
}

// ---------------------------------------------------------------- counter

test('escaping counter closure keeps its box alive', () => {
  const program = b.prog(
    b.letm(
      'makeCounter',
      b.fn([], [
        b.letm('n', b.num(0)),
        b.ret(b.fn([], [b.set('n', b.bin('+', b.vr('n'), b.num(1))), b.ret(b.vr('n'))])),
      ]),
    ),
    b.letm('c', b.call(b.vr('makeCounter'))),
    b.print(b.call(b.vr('c'))),
    b.print(b.call(b.vr('c'))),
    b.ret(b.call(b.vr('c'))),
  );
  const { interp, exec } = runBoth(program);
  check(JSON.stringify(interp.output) === JSON.stringify(['1', '2']), 'prints 1,2');
  check(exec.value === 3, 'third call returns 3');
  check(exec.stats.boxesAllocated === 1, 'exactly one box allocated');
});

// ---------------------------------------------------- siblings share a box

test('sibling closures share the same box', () => {
  const program = b.prog(
    b.letm(
      'make',
      b.fn([], [
        b.letm('n', b.num(0)),
        b.letm('inc', b.fn([], [b.set('n', b.bin('+', b.vr('n'), b.num(1))), b.ret(b.vr('n'))])),
        b.letm('get', b.fn([], [b.ret(b.vr('n'))])),
        b.ret(b.arr(b.vr('inc'), b.vr('get'))),
      ]),
    ),
    b.letm('p', b.call(b.vr('make'))),
    b.letm('inc', b.idx(b.vr('p'), b.num(0))),
    b.letm('get', b.idx(b.vr('p'), b.num(1))),
    b.expr(b.call(b.vr('inc'))),
    b.expr(b.call(b.vr('inc'))),
    b.print(b.call(b.vr('get'))),
    b.ret(b.vr('p')),
  );
  const { interp, exec } = runBoth(program);
  check(interp.output[0] === '2', 'get observes inc through the shared box');
  const pair = asClosureArray(exec.value);
  const incBox = upvalBox(pair[0], 0);
  const getBox = upvalBox(pair[1], 0);
  check(incBox !== null && incBox === getBox, 'inc and get reference the identical Box object');
});

// ------------------------------------------- call instances are separated

test('separate call instances get separate boxes', () => {
  const program = b.prog(
    b.letm(
      'makeCounter',
      b.fn([], [
        b.letm('n', b.num(0)),
        b.ret(b.fn([], [b.set('n', b.bin('+', b.vr('n'), b.num(1))), b.ret(b.vr('n'))])),
      ]),
    ),
    b.letm('c1', b.call(b.vr('makeCounter'))),
    b.letm('c2', b.call(b.vr('makeCounter'))),
    b.expr(b.call(b.vr('c1'))),
    b.expr(b.call(b.vr('c1'))),
    b.expr(b.call(b.vr('c2'))),
    b.print(b.call(b.vr('c1'))),
    b.print(b.call(b.vr('c2'))),
    b.ret(b.arr(b.vr('c1'), b.vr('c2'))),
  );
  const { interp, exec } = runBoth(program);
  check(JSON.stringify(interp.output) === JSON.stringify(['3', '2']), 'instances count independently');
  const pair = asClosureArray(exec.value);
  check(upvalBox(pair[0], 0) !== upvalBox(pair[1], 0), 'each instance owns a distinct Box');
});

// --------------------------------------- closures created inside a loop

test('loop closures capture a fresh per-iteration box', () => {
  const program = b.prog(
    b.letm('fns', b.arr()),
    b.letm('i', b.num(0)),
    b.whiles(b.bin('<', b.vr('i'), b.num(3)), [
      b.letm('j', b.bin('*', b.vr('i'), b.vr('i'))),
      b.expr(b.push(b.vr('fns'), b.fn([], [b.ret(b.vr('j'))]))),
      b.set('i', b.bin('+', b.vr('i'), b.num(1))),
    ]),
    b.print(b.call(b.idx(b.vr('fns'), b.num(0)))),
    b.print(b.call(b.idx(b.vr('fns'), b.num(1)))),
    b.print(b.call(b.idx(b.vr('fns'), b.num(2)))),
    b.ret(b.vr('fns')),
  );
  const { interp, exec } = runBoth(program);
  check(JSON.stringify(interp.output) === JSON.stringify(['0', '1', '4']), 'each closure sees its own j');
  const closures = asClosureArray(exec.value);
  const boxes = closures.map((c) => upvalBox(c, 0));
  check(boxes.every((x) => x !== null), 'all captures are boxes');
  check(boxes[0] !== boxes[1] && boxes[1] !== boxes[2] && boxes[0] !== boxes[2], 'one box per iteration');
});

test('loop closures share a box declared outside the loop', () => {
  const program = b.prog(
    b.letm('total', b.num(0)),
    b.letm('adders', b.arr()),
    b.letm('i', b.num(1)),
    b.whiles(b.bin('<=', b.vr('i'), b.num(3)), [
      b.letm('step', b.vr('i')),
      b.expr(
        b.push(
          b.vr('adders'),
          b.fn([], [b.set('total', b.bin('+', b.vr('total'), b.vr('step'))), b.ret(b.vr('total'))]),
        ),
      ),
      b.set('i', b.bin('+', b.vr('i'), b.num(1))),
    ]),
    b.expr(b.call(b.idx(b.vr('adders'), b.num(0)))),
    b.expr(b.call(b.idx(b.vr('adders'), b.num(1)))),
    b.print(b.call(b.idx(b.vr('adders'), b.num(2)))),
    b.ret(b.vr('adders')),
  );
  const { interp, exec } = runBoth(program);
  check(interp.output[0] === '6', 'shared accumulator totals 1+2+3');
  const closures = asClosureArray(exec.value);
  // freeVars are ordered by declaration id: total (upval 0), step (upval 1)
  const totals = closures.map((c) => upvalBox(c, 0));
  check(totals[0] !== null && totals[0] === totals[1] && totals[1] === totals[2], 'total box shared');
  const steps = closures.map((c) => upvalBox(c, 1));
  check(steps[0] !== steps[1] && steps[1] !== steps[2], 'step box per iteration');
});

// ---------------------------------------------------------------- recursion

test('recursion via self-name binding', () => {
  const program = b.prog(
    b.letm(
      'fact',
      b.fn(
        ['n'],
        [
          b.ifs(b.bin('<=', b.vr('n'), b.num(1)), [b.ret(b.num(1))]),
          b.ret(b.bin('*', b.vr('n'), b.call(b.vr('f'), b.bin('-', b.vr('n'), b.num(1))))),
        ],
        'f',
      ),
    ),
    b.print(b.call(b.vr('fact'), b.num(5))),
    b.ret(b.call(b.vr('fact'), b.num(10))),
  );
  const { exec } = runBoth(program);
  check(exec.value === 3628800, 'fact(10) = 3628800');
});

test('recursive helper shares one boxed accumulator across all frames', () => {
  const program = b.prog(
    b.letm('sum', b.num(0)),
    b.letm(
      'walk',
      b.fn(
        ['n'],
        [
          b.ifs(b.bin('>', b.vr('n'), b.num(0)), [
            b.set('sum', b.bin('+', b.vr('sum'), b.vr('n'))),
            b.expr(b.call(b.vr('w'), b.bin('-', b.vr('n'), b.num(1)))),
          ]),
          b.ret(b.vr('sum')),
        ],
        'w',
      ),
    ),
    b.expr(b.call(b.vr('walk'), b.num(5))),
    b.print(b.vr('sum')),
    b.ret(b.vr('sum')),
  );
  const { exec } = runBoth(program);
  check(exec.value === 15, 'sum 5..1 = 15');
  check(exec.stats.boxesAllocated === 1, 'a single box serves every recursion level');
});

test('each recursive frame gets its own box for captured locals', () => {
  const program = b.prog(
    b.letm(
      'countDown',
      b.fn(
        ['n'],
        [
          b.letm('x', b.bin('*', b.vr('n'), b.num(2))),
          b.letm('inner', b.fn([], [b.ret(b.vr('x'))])),
          b.ifs(b.bin('==', b.vr('n'), b.num(0)), [b.ret(b.call(b.vr('inner')))]),
          b.letm('r', b.call(b.vr('cd'), b.bin('-', b.vr('n'), b.num(1)))),
          b.ret(b.bin('+', b.call(b.vr('inner')), b.vr('r'))),
        ],
        'cd',
      ),
    ),
    b.print(b.call(b.vr('countDown'), b.num(3))),
    b.ret(b.call(b.vr('countDown'), b.num(3))),
  );
  const { exec } = runBoth(program);
  check(exec.value === 12, 'inner sees its own frame: 6+4+2+0 = 12');
  // top frame + 4 countDown frames (n=3..0) + the inner() call at the bottom
  check(exec.stats.maxDepth === 6, 'recursion reaches depth 6');
});

// ------------------------------------------------------- stack allocation

test('non-captured locals stay on the stack (no boxes allocated)', () => {
  const program = b.prog(
    b.letm('sum', b.num(0)),
    b.letm(
      'addTwice',
      b.fn(['x'], [
        b.letm('t', b.bin('*', b.vr('x'), b.num(2))),
        b.set('sum', b.bin('+', b.vr('sum'), b.vr('t'))),
        b.ret(b.vr('t')),
      ]),
    ),
    b.letm('i', b.num(0)),
    b.whiles(b.bin('<', b.vr('i'), b.num(5)), [
      b.expr(b.call(b.vr('addTwice'), b.vr('i'))),
      b.set('i', b.bin('+', b.vr('i'), b.num(1))),
    ]),
    b.print(b.vr('sum')),
    b.ret(b.vr('sum')),
  );
  const { exec } = runBoth(program);
  check(exec.value === 20, 'sum of 2*i for i in 0..4');
  check(exec.stats.boxesAllocated === 1, 'only the captured `sum` is boxed');
  check(exec.stats.maxDepth === 2, 'frames are popped after each call');
});

// ------------------------------------------------------------ const capture

test('captured const is copied by value, not boxed', () => {
  const program = b.prog(
    b.letc('k', b.num(10)),
    b.letm('getK', b.fn([], [b.ret(b.vr('k'))])),
    b.print(b.call(b.vr('getK'))),
    b.ret(b.vr('getK')),
  );
  const { exec } = runBoth(program);
  check(exec.stats.boxesAllocated === 0, 'no box allocated for a const capture');
  check(isClosure(exec.value), 'program returns the closure');
  const upval = (exec.value as TClosure).upvals[0];
  check(upval.kind === 'const' && upval.value === 10, 'const captured by value');
});

test('assignment to a const is a compile error', () => {
  let threw = false;
  try {
    compile(b.prog(b.letc('k', b.num(1)), b.set('k', b.num(2))));
  } catch (err) {
    threw = /const/.test((err as Error).message);
  }
  check(threw, 'compile() rejects writes to const bindings');
});

// ---------------------------------------------------------------- shadowing

test('shadowed bindings capture the right declaration', () => {
  const program = b.prog(
    b.letm('x', b.num(1)),
    b.letm('outer', b.fn([], [b.ret(b.vr('x'))])),
    b.block(
      b.letm('x', b.num(20)),
      b.letm('inner', b.fn([], [b.ret(b.vr('x'))])),
      b.print(b.call(b.vr('inner'))),
      b.set('x', b.num(21)),
      b.print(b.call(b.vr('inner'))),
    ),
    b.print(b.call(b.vr('outer'))),
    b.ret(b.call(b.vr('outer'))),
  );
  const { interp, exec } = runBoth(program);
  check(JSON.stringify(interp.output) === JSON.stringify(['20', '21', '1']), 'inner/outer see their own x');
  check(exec.value === 1, 'outer x unaffected by the shadowing block');
});

// ------------------------------------------------------------------ summary

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
