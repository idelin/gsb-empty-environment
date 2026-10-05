import { test } from 'node:test';
import assert from 'node:assert/strict';
import { UNKNOWN } from '../src/lang.ts';
import { setup, assertMatchesReference, rootErrors } from './helpers.ts';

test('常量链：const 可引用前面可见的声明并折叠；跨属性依赖链被显式记录', () => {
  const { prog, ev } = setup();
  // const a = 1; const b = a + 2; const c = b * 3;
  const a = prog.constDecl('a', prog.num(1));
  const refA = prog.ref('a');
  const sumB = prog.bin('+', refA, prog.num(2));
  const b = prog.constDecl('b', sumB);
  const refB = prog.ref('b');
  const prodC = prog.bin('*', refB, prog.num(3));
  const c = prog.constDecl('c', prodC);
  prog.setRoot(prog.block(a, b, c));

  assert.equal(ev.get(sumB.id, 'const'), 3);
  assert.equal(ev.get(prodC.id, 'const'), 9);

  // 跨属性依赖路径（综合 const -> 综合 binding -> 继承 envIn -> 综合 envOut -> 综合 const）：
  //   prodC.const -> refB.const -> refB.binding -> refB.envIn -> c.envIn -> b.envOut -> sumB.const
  assert.deepStrictEqual(
    ev.dependenciesOf(refB.id, 'const').map((d) => d.key),
    [`${refB.id}.binding`],
  );
  assert.deepStrictEqual(
    ev.dependenciesOf(refB.id, 'binding').map(({ key, via }) => ({ key, via })),
    [{ key: `${refB.id}.envIn`, via: 'lookup:b' }],
  );
  assert.deepStrictEqual(
    ev.dependenciesOf(refB.id, 'envIn').map((d) => d.key),
    [`${c.id}.envIn`],
  );
  assert.deepStrictEqual(
    ev.dependenciesOf(c.id, 'envIn').map((d) => d.key),
    [`${b.id}.envOut`],
  );
  const envOutDeps = ev.dependenciesOf(b.id, 'envOut').map((d) => d.key);
  assert.ok(envOutDeps.includes(`${b.id}.envIn`));
  assert.ok(envOutDeps.includes(`${sumB.id}.type`));
  assert.ok(envOutDeps.includes(`${sumB.id}.const`));
  assertMatchesReference(prog, ev);
});

test('布尔/比较/逻辑运算的类型与常量折叠', () => {
  const { prog, ev } = setup();
  const t = prog.constDecl('t', prog.bool(true));
  const notT = prog.not(prog.ref('t'));
  const lt = prog.bin('<', prog.num(1), prog.num(2));
  const eq = prog.bin('==', notT, prog.bool(false));
  const and = prog.bin('and', lt, eq);
  prog.setRoot(prog.block(t, prog.exprStmt(and)));

  assert.equal(ev.get(notT.id, 'const'), false);
  assert.equal(ev.get(lt.id, 'type'), 'bool');
  assert.equal(ev.get(lt.id, 'const'), true);
  assert.equal(ev.get(and.id, 'const'), true);
  assert.equal(rootErrors(prog, ev).length, 0);
  assertMatchesReference(prog, ev);
});

test('类型错误报告在出错节点的 type 属性上；非常量操作数使 const 为 UNKNOWN', () => {
  const { prog, ev } = setup();
  const bad = prog.bin('+', prog.num(1), prog.bool(true));
  const refP = prog.ref('p');
  const body = prog.block(prog.exprStmt(prog.bin('*', refP, prog.num(2))));
  const f = prog.funcDecl('g', [['p', 'int']], body);
  prog.setRoot(prog.block(prog.exprStmt(bad), f));

  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].node, bad.id);
  assert.equal(errs[0].attr, 'type');
  assert.match(errs[0].message, /'\+' cannot be applied to int and bool/);
  assert.equal(ev.get(refP.id, 'const'), UNKNOWN);
  assertMatchesReference(prog, ev);
});

test('引用未绑定名字时 const 为 UNKNOWN、type 为 error', () => {
  const { prog, ev } = setup();
  const r = prog.ref('missing');
  prog.setRoot(prog.block(prog.exprStmt(r)));
  assert.equal(ev.get(r.id, 'const'), UNKNOWN);
  assert.equal(ev.get(r.id, 'type'), 'error');
  assertMatchesReference(prog, ev);
});
