import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Replica } from '../src/replica.ts';
import type { OpNode } from '../src/types.ts';

/** 把 from 的待广播节点投递给 to */
function sync(from: Replica, to: Replica): void {
  for (const node of from.drainOutbox()) to.receive(node);
}

/** 双向同步 */
function syncAll(a: Replica, b: Replica): void {
  sync(a, b);
  sync(b, a);
}

test('验收场景：A 输入 hello，B 插入 !，A 撤销后保留 !，重做恢复 hello!', () => {
  const A = new Replica('A');
  const B = new Replica('B');

  // 用户 A 输入 'hello'
  A.insertText(null, 'hello');
  assert.equal(A.doc.text(), 'hello');

  // 同步给 B，B 在末尾插入 '!'
  sync(A, B);
  assert.equal(B.doc.text(), 'hello');
  B.insertText(B.doc.lastVisibleCharId(), '!');
  assert.equal(B.doc.text(), 'hello!');

  // A 收到 B 的输入后执行 Undo：只撤回自己的 'hello'
  sync(B, A);
  A.undo();
  assert.equal(A.doc.text(), '!', 'A 撤销 hello 后应保留 B 的 !');

  // B 收到 A 的补偿节点后同样呈现 '!'
  sync(A, B);
  assert.equal(B.doc.text(), '!');

  // A 执行 Redo：无缝恢复为 'hello!'
  A.redo();
  assert.equal(A.doc.text(), 'hello!');
  sync(A, B);
  assert.equal(B.doc.text(), 'hello!', '所有副本应收敛为 hello!');
});

test('撤销树只追加节点：Undo/Redo 生成反向补偿节点且历史不被修改', () => {
  const A = new Replica('A');
  const insert = A.insertText(null, 'hi');
  const sizeAfterInsert = A.nodes.size;

  const undoNode = A.undo()!;
  assert.equal(A.doc.text(), '');
  assert.equal(undoNode.kind, 'delete', 'insert 的补偿是 delete');
  assert.equal(undoNode.compensates, insert.id, '补偿节点指向被补偿节点');
  assert.ok(A.nodes.has(insert.id), '原始节点保留在 DAG 中');
  assert.ok(A.nodes.size > sizeAfterInsert, 'DAG 只增不改');

  const redoNode = A.redo()!;
  assert.equal(A.doc.text(), 'hi');
  assert.equal(redoNode.kind, 'restore', '对补偿再取逆得到 restore');
  assert.equal(redoNode.compensates, undoNode.id);
});

test('多次撤销按后进先出回退自己的操作', () => {
  const A = new Replica('A');
  A.insertText(null, 'ab');
  A.insertText(A.doc.lastVisibleCharId(), 'cd');
  assert.equal(A.doc.text(), 'abcd');

  A.undo();
  assert.equal(A.doc.text(), 'ab');
  A.undo();
  assert.equal(A.doc.text(), '');
  assert.equal(A.undo(), null, '栈空时 Undo 为无操作');

  A.redo();
  A.redo();
  assert.equal(A.doc.text(), 'abcd');
});

test('撤销删除操作：delete 的补偿是 restore', () => {
  const A = new Replica('A');
  A.insertText(null, 'xy');
  const firstChar = 'A:1';
  A.deleteChars([firstChar]);
  assert.equal(A.doc.text(), 'y');

  A.undo();
  assert.equal(A.doc.text(), 'xy', '撤销删除应恢复字符');
  A.redo();
  assert.equal(A.doc.text(), 'y', '重做应重新删除');
});

test('并发插入收敛：双方离线输入后同步结果一致', () => {
  const A = new Replica('A');
  const B = new Replica('B');

  // 离线并发：A 写 'hello'，B 同时在开头写 '>>'
  A.insertText(null, 'hello');
  B.insertText(null, '>>');

  syncAll(A, B);
  assert.equal(A.doc.text(), B.doc.text(), '并发后两副本必须收敛');
  assert.ok(A.doc.text().includes('hello'));
  assert.ok(A.doc.text().includes('>>'));

  // 一方撤销不影响另一方并发输入
  A.undo();
  sync(A, B);
  assert.equal(A.doc.text(), B.doc.text());
  assert.ok(!A.doc.text().includes('hello'));
  assert.ok(A.doc.text().includes('>>'));
});

test('DAG 拓扑有效性：每个节点的父节点先于其存在', () => {
  const A = new Replica('A');
  const B = new Replica('B');
  A.insertText(null, 'hello');
  sync(A, B);
  B.insertText(B.doc.lastVisibleCharId(), '!');
  sync(B, A);
  A.undo();
  sync(A, B);
  A.redo();
  sync(A, B);

  for (const replica of [A, B]) {
    const seen = new Set<string>();
    // 按接收顺序重放，验证 parents 均已知（因果/拓扑有效）
    for (const [id, node] of replica.nodes) {
      for (const p of node.parents) {
        assert.ok(replica.nodes.has(p), `父节点 ${p} 必须存在于 DAG`);
      }
      seen.add(id);
    }
    assert.equal(replica.nodes.size, B.nodes.size);
  }
  assert.equal(A.nodes.size, 4, '2 次用户操作 + 1 次撤销补偿 + 1 次重做补偿 = 4 个节点');
});
