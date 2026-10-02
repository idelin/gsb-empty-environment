import { test } from 'node:test';
import assert from 'node:assert/strict';
import { UndoTree } from '../src/undoTree.ts';

test('验收场景: A 撤销 hello 保留 B 的 !, redo 恢复 hello!', () => {
  const doc = new UndoTree('r1');
  doc.insert('A', null, 'hello');
  doc.insert('B', doc.tailElementId(), '!');
  assert.equal(doc.text(), 'hello!');

  doc.undo('A');
  assert.equal(doc.text(), '!');

  doc.redo('A');
  assert.equal(doc.text(), 'hello!');
});

test('undo 追加反向补偿节点并推进图头部', () => {
  const doc = new UndoTree('r1');
  const insert = doc.insert('A', null, 'hello');
  doc.insert('B', doc.tailElementId(), '!');
  const headsBefore = doc.heads();

  const compensation = doc.undo('A');
  assert.ok(compensation);
  assert.equal(compensation.kind, 'delete');
  assert.equal(compensation.compensates, insert.id);
  assert.deepEqual(compensation.parents, headsBefore);
  assert.deepEqual(doc.heads(), [compensation.id]);
});

test('拓扑有效性: 所有父引用可解析且无环', () => {
  const doc = new UndoTree('r1');
  doc.insert('A', null, 'hello');
  doc.insert('B', doc.tailElementId(), '!');
  doc.undo('A');
  doc.redo('A');
  doc.undo('A');
  assert.ok(doc.validateTopology());
  assert.equal(doc.getOps().length, 5);
});

test('空历史 undo/redo 是安全的 no-op', () => {
  const doc = new UndoTree('r1');
  assert.equal(doc.undo('A'), null);
  assert.equal(doc.redo('A'), null);
  assert.equal(doc.text(), '');
  assert.equal(doc.getOps().length, 0);
});

test('同一用户可连续撤销多步', () => {
  const doc = new UndoTree('r1');
  doc.insert('A', null, 'x');
  doc.insert('A', doc.tailElementId(), 'y');
  assert.equal(doc.text(), 'xy');
  doc.undo('A');
  assert.equal(doc.text(), 'x');
  doc.undo('A');
  assert.equal(doc.text(), '');
  assert.equal(doc.undo('A'), null);
});

test('新编辑清空该用户的 redo 栈', () => {
  const doc = new UndoTree('r1');
  doc.insert('A', null, 'hello');
  doc.undo('A');
  doc.insert('A', doc.tailElementId(), 'world');
  assert.equal(doc.redo('A'), null);
  assert.equal(doc.text(), 'world');
});

test('各用户独立撤销, 互不影响', () => {
  const doc = new UndoTree('r1');
  doc.insert('A', null, 'hello');
  doc.insert('B', doc.tailElementId(), '!');
  doc.undo('B');
  assert.equal(doc.text(), 'hello');
  doc.undo('A');
  assert.equal(doc.text(), '');
});

test('显式 delete 也可被 undo 恢复', () => {
  const doc = new UndoTree('r1');
  const insert = doc.insert('A', null, 'hello');
  doc.delete('A', insert.elementId);
  assert.equal(doc.text(), '');
  doc.undo('A');
  assert.equal(doc.text(), 'hello');
});

test('墓碑元素仍作为并发插入的锚点', () => {
  const doc = new UndoTree('r1');
  const a = doc.insert('A', null, 'ab');
  doc.insert('B', a.elementId, 'CD');
  assert.equal(doc.text(), 'abCD');
  doc.undo('A');
  assert.equal(doc.text(), 'CD');
});

test('双副本交换操作后收敛到相同状态', () => {
  const r1 = new UndoTree('r1');
  const r2 = new UndoTree('r2');

  const aInsert = r1.insert('A', null, 'hello');
  r2.receive(aInsert);
  const bInsert = r2.insert('B', r2.tailElementId(), '!');
  r1.receive(bInsert);
  assert.equal(r1.text(), 'hello!');
  assert.equal(r2.text(), 'hello!');

  const undoOp = r1.undo('A')!;
  r2.receive(undoOp);
  assert.equal(r1.text(), '!');
  assert.equal(r2.text(), '!');

  const redoOp = r1.redo('A')!;
  r2.receive(redoOp);
  assert.equal(r1.text(), 'hello!');
  assert.equal(r2.text(), 'hello!');
  assert.ok(r2.validateTopology());
});

test('乱序投递: 父节点缺失时缓冲, 到齐后生效', () => {
  const r1 = new UndoTree('r1');
  const r2 = new UndoTree('r2');

  const first = r1.insert('A', null, 'hello');
  const second = r1.insert('A', r1.tailElementId(), ' world');

  r2.receive(second); // 父节点 first 未到, 进入缓冲
  assert.equal(r2.text(), '');
  assert.equal(r2.getOps().length, 0);

  r2.receive(first); // 到齐后按因果序重放
  assert.equal(r2.text(), 'hello world');
  assert.equal(r2.getOps().length, 2);
  assert.ok(r2.validateTopology());
});
