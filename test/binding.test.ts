import { test } from 'node:test';
import assert from 'node:assert/strict';
import { UNKNOWN } from '../src/lang.ts';
import { setup, assertMatchesReference, rootErrors } from './helpers.ts';

test('声明从其后语句起可见；之前引用未绑定', () => {
  const { prog, ev } = setup();
  const refBefore = prog.ref('x');
  const useBefore = prog.exprStmt(refBefore);
  const decl = prog.constDecl('x', prog.num(1));
  const refAfter = prog.ref('x');
  const sum = prog.bin('+', refAfter, prog.num(2));
  const useAfter = prog.exprStmt(sum);
  prog.setRoot(prog.block(useBefore, decl, useAfter));

  assert.equal(ev.get(refBefore.id, 'type'), 'error');
  assert.equal(ev.get(sum.id, 'const'), 3);
  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.deepStrictEqual(errs[0], {
    node: refBefore.id,
    attr: 'binding',
    message: "unbound variable 'x'",
  });
  assertMatchesReference(prog, ev);
});

test('内层遮蔽外层，外层在块后仍然有效', () => {
  const { prog, ev } = setup();
  const outerX = prog.constDecl('x', prog.num(1));
  const innerX = prog.constDecl('x', prog.bool(true));
  const innerRef = prog.ref('x');
  const inner = prog.block(innerX, prog.exprStmt(innerRef));
  const outerRef = prog.ref('x');
  const outerUse = prog.exprStmt(outerRef);
  prog.setRoot(prog.block(outerX, inner, outerUse));

  assert.equal(ev.get(innerRef.id, 'type'), 'bool');
  assert.equal(ev.get(innerRef.id, 'const'), true);
  assert.equal(ev.get(outerRef.id, 'type'), 'int');
  assert.equal(ev.get(outerRef.id, 'const'), 1);
  assert.equal(rootErrors(prog, ev).length, 0);
  assertMatchesReference(prog, ev);
});

test('同块重复声明在 envOut 属性上报错，并指出首个声明节点', () => {
  const { prog, ev } = setup();
  const d1 = prog.constDecl('x', prog.num(1));
  const d2 = prog.constDecl('x', prog.num(2));
  const root = prog.block(d1, d2);
  prog.setRoot(root);

  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].node, d2.id);
  assert.equal(errs[0].attr, 'envOut');
  assert.match(errs[0].message, /duplicate declaration of 'x'/);
  assert.match(errs[0].message, new RegExp(`block ${root.id}\\b`));
  assert.match(errs[0].message, new RegExp(`first declared at ${d1.id}\\b`));
  assertMatchesReference(prog, ev);
});

test('嵌套函数：参数进入函数体作用域，外层声明对函数体可见，参数不是常量', () => {
  const { prog, ev } = setup();
  const a = prog.constDecl('a', prog.num(7));
  const refA = prog.ref('a');
  const g = prog.funcDecl('g', [['q', 'bool']], prog.block(prog.exprStmt(refA)));
  const refP = prog.ref('p');
  const f = prog.funcDecl('f', [['p', 'int']], prog.block(prog.exprStmt(refP), g));
  prog.setRoot(prog.block(a, f));

  assert.equal(ev.get(refA.id, 'const'), 7);
  assert.equal(ev.get(refP.id, 'type'), 'int');
  assert.equal(ev.get(refP.id, 'const'), UNKNOWN);
  assert.equal(rootErrors(prog, ev).length, 0);
  assertMatchesReference(prog, ev);
});

test('缓存键是 (节点ID, 属性名) 而非变量名：同名引用各有独立缓存项', () => {
  const { prog, ev } = setup();
  const d = prog.constDecl('x', prog.num(1));
  const r1 = prog.ref('x');
  const r2 = prog.ref('x');
  prog.setRoot(prog.block(d, prog.exprStmt(r1), prog.exprStmt(r2)));
  rootErrors(prog, ev);

  assert.notEqual(r1.id, r2.id);
  assert.ok(ev.has(r1.id, 'binding'));
  assert.ok(ev.has(r2.id, 'binding'));
  assert.deepStrictEqual(ev.peek(r1.id, 'binding'), ev.peek(r2.id, 'binding'));
});

test('未绑定引用也记录对作用域的依赖（via=lookup:<名>）', () => {
  const { prog, ev } = setup();
  const r = prog.ref('y');
  const use = prog.exprStmt(r);
  prog.setRoot(prog.block(use));

  assert.equal(rootErrors(prog, ev).length, 1);
  const depKeys = ev.dependenciesOf(r.id, 'binding').map(({ key, via }) => ({ key, via }));
  assert.deepStrictEqual(depKeys, [{ key: `${r.id}.envIn`, via: 'lookup:y' }]);
  assert.deepStrictEqual(
    ev.dependenciesOf(r.id, 'envIn').map((d) => d.key),
    [`${use.id}.envIn`],
  );
});
