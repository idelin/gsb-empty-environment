import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyEdit } from '../src/edits.ts';
import { snapshotIncremental } from '../src/lang.ts';
import { setup, assertMatchesReference, rootErrors } from './helpers.ts';

test('移动声明跨过引用：错误随位置出现/消失，节点 ID 不变', () => {
  const { prog, ev } = setup();
  const declX = prog.constDecl('x', prog.num(1));
  const refX = prog.ref('x');
  const useX = prog.exprStmt(refX);
  const root = prog.block(declX, useX);
  prog.setRoot(root);

  assert.equal(rootErrors(prog, ev).length, 0);
  assert.equal(ev.get(refX.id, 'const'), 1);

  // 声明移到引用之后：引用变未绑定
  applyEdit(prog, ev, { op: 'move', stmt: declX.id, toBlock: root.id, index: 1 });
  assert.equal(prog.node(declX.id), declX); // ID 与节点对象不变
  assert.deepStrictEqual(root.stmts.map((s) => s.id), [useX.id, declX.id]);
  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.deepStrictEqual(errs[0], {
    node: refX.id,
    attr: 'binding',
    message: "unbound variable 'x'",
  });
  assert.equal(ev.get(refX.id, 'type'), 'error');
  assertMatchesReference(prog, ev);

  // 移回引用之前：错误修复
  applyEdit(prog, ev, { op: 'move', stmt: declX.id, toBlock: root.id, index: 0 });
  assert.equal(rootErrors(prog, ev).length, 0);
  assert.equal(ev.get(refX.id, 'const'), 1);
  assertMatchesReference(prog, ev);
});

test('删除外层定义：内层引用变未绑定；插入同名声明后修复（负依赖）', () => {
  const { prog, ev } = setup();
  const declX = prog.constDecl('x', prog.num(1));
  const refX = prog.ref('x');
  const inner = prog.block(prog.exprStmt(refX));
  const root = prog.block(declX, inner);
  prog.setRoot(root);
  assert.equal(rootErrors(prog, ev).length, 0);

  applyEdit(prog, ev, { op: 'remove', stmt: declX.id });
  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.deepStrictEqual(errs[0], {
    node: refX.id,
    attr: 'binding',
    message: "unbound variable 'x'",
  });
  assertMatchesReference(prog, ev);

  // 插入新声明（全新节点 ID）即可修复之前记录的未绑定引用
  applyEdit(prog, ev, {
    op: 'insert',
    block: root.id,
    index: 0,
    stmt: { kind: 'const', name: 'x', expr: { kind: 'num', value: 42 } },
  });
  assert.equal(rootErrors(prog, ev).length, 0);
  assert.equal(ev.get(refX.id, 'const'), 42);
  assertMatchesReference(prog, ev);
});

test('删除内层遮蔽声明后，引用解析到外层绑定', () => {
  const { prog, ev } = setup();
  const outer = prog.constDecl('x', prog.num(1));
  const innerDecl = prog.constDecl('x', prog.bool(true));
  const refX = prog.ref('x');
  const inner = prog.block(innerDecl, prog.exprStmt(refX));
  const root = prog.block(outer, inner);
  prog.setRoot(root);
  assert.equal(ev.get(refX.id, 'type'), 'bool');

  applyEdit(prog, ev, { op: 'remove', stmt: innerDecl.id });
  assert.equal(ev.get(refX.id, 'type'), 'int');
  assert.equal(ev.get(refX.id, 'const'), 1);
  assert.equal(rootErrors(prog, ev).length, 0);
  assertMatchesReference(prog, ev);
});

test('参数改名：旧名引用变未绑定，新名引用生效', () => {
  const { prog, ev } = setup();
  const refP = prog.ref('p');
  const refQ = prog.ref('q');
  const f = prog.funcDecl('f', [['p', 'int']], prog.block(prog.exprStmt(refP), prog.exprStmt(refQ)));
  prog.setRoot(prog.block(f));

  let errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].node, refQ.id); // q 未绑定

  applyEdit(prog, ev, { op: 'renameParam', param: f.params[0].id, name: 'q' });
  errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].node, refP.id); // 现在 p 未绑定
  assert.equal(ev.get(refQ.id, 'type'), 'int');
  assertMatchesReference(prog, ev);
});

test('批量 AST 编辑：一次应用多处变更，结果与参考版一致', () => {
  const { prog, ev } = setup();
  const declA = prog.constDecl('a', prog.num(1));
  const refA = prog.ref('a');
  const useA = prog.exprStmt(refA);
  const declB = prog.constDecl('b', prog.num(2));
  const root = prog.block(declA, useA, declB);
  prog.setRoot(root);
  assert.equal(rootErrors(prog, ev).length, 0);

  applyEdit(prog, ev, {
    op: 'batch',
    edits: [
      { op: 'remove', stmt: declB.id },
      { op: 'insert', block: root.id, index: 0,
        stmt: { kind: 'const', name: 'c', expr: { kind: 'num', value: 5 } } },
      { op: 'move', stmt: useA.id, toBlock: root.id, index: 0 },
      { op: 'insert', block: root.id, index: 3,
        stmt: { kind: 'expr', expr: { kind: 'ref', name: 'c' } } },
    ],
  });

  // 结果结构：[useA, c=5, declA, useC]；useA 在 declA 之前 -> a 未绑定
  const errs = rootErrors(prog, ev);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].node, refA.id);
  assertMatchesReference(prog, ev);
});

test('增量性：块内编辑不重算兄弟块的任何属性', () => {
  const { prog, ev } = setup();
  const a1 = prog.constDecl('a', prog.num(1));
  const blockA = prog.block(a1, prog.exprStmt(prog.bin('+', prog.ref('a'), prog.num(1))));
  const b1 = prog.constDecl('b', prog.bool(true));
  const blockB = prog.block(b1, prog.exprStmt(prog.not(prog.ref('b'))));
  const root = prog.block(blockA, blockB);
  prog.setRoot(root);
  snapshotIncremental(prog, ev); // 全量求值
  ev.resetStats();

  applyEdit(prog, ev, {
    op: 'insert',
    block: blockA.id,
    index: 2,
    stmt: { kind: 'const', name: 'a2', expr: { kind: 'num', value: 9 } },
  });
  snapshotIncremental(prog, ev); // 触发增量重算

  const blockBIds = new Set(prog.subtreeIds(blockB.id));
  const recomputedInB = ev.lastRecomputed.filter((k) => blockBIds.has(k.split('.')[0]));
  assert.deepStrictEqual(recomputedInB, []);
  assert.ok(ev.stats.verified > 0); // 有属性通过 verify 复用旧值
  assertMatchesReference(prog, ev);
});

test('增量性：块尾追加声明只重算环境骨架，既有表达式属性被 verify 剪枝', () => {
  const { prog, ev } = setup();
  const declX = prog.constDecl('x', prog.num(1));
  const refX = prog.ref('x');
  const sum = prog.bin('+', refX, prog.num(2));
  const useX = prog.exprStmt(sum);
  const root = prog.block(declX, useX);
  prog.setRoot(root);
  snapshotIncremental(prog, ev);
  ev.resetStats();

  applyEdit(prog, ev, {
    op: 'insert',
    block: root.id,
    index: 2,
    stmt: { kind: 'const', name: 'y', expr: { kind: 'num', value: 3 } },
  });
  snapshotIncremental(prog, ev);

  assert.ok(!ev.lastRecomputed.includes(`${refX.id}.binding`));
  assert.ok(!ev.lastRecomputed.includes(`${sum.id}.type`));
  assert.ok(!ev.lastRecomputed.includes(`${sum.id}.const`));
  assert.ok(ev.stats.verified > 0);
  assertMatchesReference(prog, ev);
});
