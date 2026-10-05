import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CycleError } from '../src/evaluator.ts';
import { setup, assertMatchesReference, rootErrors } from './helpers.ts';

test('跨属性循环：block 可见性下 const 互相引用，报告具体依赖路径', () => {
  const { prog, ev } = setup('block');
  // const a = b; const b = a;  （块内所有声明一次可见 -> 成环）
  const refB = prog.ref('b');
  const declA = prog.constDecl('a', refB);
  const refA = prog.ref('a');
  const declB = prog.constDecl('b', refA);
  const root = prog.block(declA, declB);
  prog.setRoot(root);

  let err: unknown;
  try {
    ev.get(root.id, 'errors');
  } catch (e) {
    err = e;
  }
  assert.ok(err instanceof CycleError);
  // 具体路径：继承属性 envIn -> 综合属性 blockEnv -> type -> binding -> envIn -> 回到起点
  assert.deepStrictEqual((err as CycleError).path, [
    `${declA.id}.envIn`,
    `${root.id}.blockEnv`,
    `${refB.id}.type`,
    `${refB.id}.binding`,
    `${refB.id}.envIn`,
    `${declA.id}.envIn`,
  ]);
  assert.match((err as CycleError).message, /circular attribute dependency/);
});

test('顺序可见性下同样的程序不成环，而是未绑定错误', () => {
  const { prog, ev } = setup();
  const refB = prog.ref('b');
  const declA = prog.constDecl('a', refB);
  const refA = prog.ref('a');
  const declB = prog.constDecl('b', refA);
  const root = prog.block(declA, declB);
  prog.setRoot(root);

  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].node, refB.id);
  assert.equal(errs[0].attr, 'binding');
  assertMatchesReference(prog, ev);
});
