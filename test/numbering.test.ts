import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LevelJumpError, OutlineStore } from '../src/index.ts';
import type { Heading } from '../src/index.ts';

function doc(): Heading[] {
  return [
    { id: 'c1', title: '第一章', level: 1 },
    { id: 's1', title: '第一节', level: 2 },
    { id: 's2', title: '第二节', level: 2 },
    { id: 'c2', title: '第二章', level: 1 },
  ];
}

test('初始编号按树形层级生成', () => {
  const store = new OutlineStore(doc());
  assert.equal(store.getNumber('c1'), '1');
  assert.equal(store.getNumber('s1'), '1.1');
  assert.equal(store.getNumber('s2'), '1.2');
  assert.equal(store.getNumber('c2'), '2');
  assert.equal(store.version, 1);
});

test('重命名不改变编号，稳定 ID 不变', () => {
  const store = new OutlineStore(doc());
  const result = store.applyEdit({ kind: 'rename', id: 's1', title: ' renamed ' });
  assert.equal(store.getNumber('s1'), '1.1');
  assert.deepEqual(result.changes, []);
  assert.equal(store.getHeadings().find((h) => h.id === 's1')!.title, ' renamed ');
});

test('改级别触发增量重编号，只输出受影响的编号变化', () => {
  const store = new OutlineStore(doc());
  const result = store.applyEdit({ kind: 'changeLevel', id: 's2', level: 1 });
  assert.equal(store.getNumber('s2'), '2');
  assert.equal(store.getNumber('c2'), '3');
  assert.deepEqual(
    result.changes.map((c) => [c.id, c.oldNumber, c.newNumber]),
    [
      ['s2', '1.2', '2'],
      ['c2', '2', '3'],
    ],
  );
});

test('禁止跳级：report 模式给出最小修复范围且不擅自改正文', () => {
  const store = new OutlineStore(doc());
  const result = store.applyEdit({
    kind: 'insert',
    heading: { id: 'x', title: '突兀的三级标题', level: 3 },
    position: { afterId: 'c1' },
  });
  assert.equal(result.violations.length, 1);
  const v = result.violations[0];
  assert.equal(v.headingId, 'x');
  assert.equal(v.actualLevel, 3);
  assert.equal(v.maxAllowedLevel, 2);
  assert.deepEqual(v.fixRange.headingIds, ['x']);
  assert.deepEqual(v.suggestion, { kind: 'shift-subtree', delta: -1 });
  assert.deepEqual(v.alternative, { kind: 'insert-ancestors', count: 1 });
  // 正文未被擅自修改：级别仍是 3，编号为 null
  assert.equal(store.getHeadings().find((h) => h.id === 'x')!.level, 3);
  assert.equal(store.getNumber('x'), null);
  // 其余标题编号不受影响
  assert.equal(store.getNumber('s1'), '1.1');
});

test('跳级修复范围覆盖整个子树（最小范围）', () => {
  const store = new OutlineStore([{ id: 'a', title: 'A', level: 1 }]);
  store.applyEdit({ kind: 'insert', heading: { id: 'b', title: 'B', level: 3 } });
  store.applyEdit({ kind: 'insert', heading: { id: 'c', title: 'C', level: 4 } });
  const violations = store.getViolations();
  assert.equal(violations.length, 1);
  assert.equal(violations[0].headingId, 'b');
  assert.deepEqual(violations[0].fixRange.headingIds, ['b', 'c']);
  assert.deepEqual(violations[0].suggestion, { kind: 'shift-subtree', delta: -1 });
});

test('禁止跳级：reject 模式拒绝编辑且状态不变', () => {
  const store = new OutlineStore(doc());
  assert.throws(
    () =>
      store.applyEdit(
        { kind: 'insert', heading: { id: 'x', title: 'X', level: 3 }, position: { afterId: 'c1' } },
        { onLevelJump: 'reject' },
      ),
    (err: unknown) => {
      assert.ok(err instanceof LevelJumpError);
      assert.equal(err.violations.length, 1);
      assert.equal(err.violations[0].headingId, 'x');
      return true;
    },
  );
  assert.equal(store.version, 1);
  assert.equal(store.getNumber('x'), undefined);
});

test('删除父标题：子标题成为孤儿，报跳级与编号变化，不擅自改级别', () => {
  const store = new OutlineStore(doc());
  store.addReference({ id: 'r-c1', targetId: 'c1' });
  const result = store.applyEdit({ kind: 'remove', id: 'c1' });

  // 孤儿 s1/s2 仍为 2 级（正文未被改动），编号失效并给出最小修复范围
  assert.equal(store.getHeadings().find((h) => h.id === 's1')!.level, 2);
  assert.deepEqual(
    result.violations.map((v) => [v.headingId, v.fixRange.headingIds, v.suggestion.delta]),
    [
      ['s1', ['s1'], -1],
      ['s2', ['s2'], -1],
    ],
  );

  // 编号变化：c1 移除、s1/s2 失效、c2 从 2 变 1
  assert.deepEqual(
    result.changes.map((c) => [c.id, c.oldNumber, c.newNumber, c.kind]),
    [
      ['s1', '1.1', null, 'changed'],
      ['s2', '1.2', null, 'changed'],
      ['c2', '2', '1', 'changed'],
      ['c1', '1', null, 'removed'],
    ],
  );

  // 指向 c1 的引用成为悬空引用，附带最后已知编号
  assert.deepEqual(
    result.danglingReferences.map((d) => [d.referenceId, d.lastKnown?.number, d.lastKnown?.title]),
    [['r-c1', '1', '第一章']],
  );
});

test('删除父标题（含子树）：子标题引用一并悬空', () => {
  const store = new OutlineStore(doc());
  store.addReference({ id: 'r-s1', targetId: 's1' });
  store.addReference({ id: 'r-c2', targetId: 'c2' });
  const result = store.applyEdit({ kind: 'remove', id: 'c1', withSubtree: true });
  assert.deepEqual(
    store.getHeadings().map((h) => h.id),
    ['c2'],
  );
  assert.equal(store.getNumber('c2'), '1');
  assert.deepEqual(
    result.danglingReferences.map((d) => d.referenceId),
    ['r-s1'],
  );
  assert.equal(store.resolveReference('r-c2').status, 'ok');
  assert.deepEqual(
    store.listDanglingReferences().map((d) => d.referenceId),
    ['r-s1'],
  );
});

test('同名标题：引用按稳定 ID 各自解析', () => {
  const store = new OutlineStore([
    { id: 'c1', title: '指南', level: 1 },
    { id: 'a1', title: '概述', level: 2 },
    { id: 'c2', title: '附录', level: 1 },
    { id: 'a2', title: '概述', level: 2 },
  ]);
  store.addReference({ id: 'r1', targetId: 'a1' });
  store.addReference({ id: 'r2', targetId: 'a2' });

  assert.equal(store.findByTitle('概述').length, 2);

  const r1 = store.resolveReference('r1');
  const r2 = store.resolveReference('r2');
  assert.ok(r1.status === 'ok' && r1.number === '1.1');
  assert.ok(r2.status === 'ok' && r2.number === '2.1');

  // 重命名其中一个，另一个引用不受影响
  store.applyEdit({ kind: 'rename', id: 'a1', title: '总览' });
  const r2After = store.resolveReference('r2');
  assert.ok(r2After.status === 'ok' && r2After.number === '2.1' && r2After.title === '概述');
});

test('跨章节移动：子树整体重编号，引用仍指向稳定 ID', () => {
  const store = new OutlineStore([
    { id: 'c1', title: '第一章', level: 1 },
    { id: 's1', title: '背景', level: 2 },
    { id: 'd1', title: '细节', level: 3 },
    { id: 's2', title: '方法', level: 2 },
    { id: 'c2', title: '第二章', level: 1 },
    { id: 's3', title: '结果', level: 2 },
  ]);
  store.addReference({ id: 'r-s1', targetId: 's1' });

  const result = store.applyEdit({ kind: 'move', id: 's1', to: { beforeId: 's3' } });

  assert.deepEqual(
    store.getHeadings().map((h) => h.id),
    ['c1', 's2', 'c2', 's1', 'd1', 's3'],
  );
  // 子树整体搬迁并重新编号
  assert.equal(store.getNumber('s1'), '2.1');
  assert.equal(store.getNumber('d1'), '2.1.1');
  assert.equal(store.getNumber('s2'), '1.1');
  assert.equal(store.getNumber('s3'), '2.2');
  // 增量输出只包含受影响的标题
  assert.deepEqual(
    result.changes.map((c) => [c.id, c.oldNumber, c.newNumber]),
    [
      ['s2', '1.2', '1.1'],
      ['s1', '1.1', '2.1'],
      ['d1', '1.1.1', '2.1.1'],
      ['s3', '2.1', '2.2'],
    ],
  );
  // 引用仍解析到同一标题，编号跟随移动
  const resolved = store.resolveReference('r-s1');
  assert.ok(resolved.status === 'ok' && resolved.number === '2.1' && resolved.title === '背景');
  assert.equal(result.violations.length, 0);
});

test('移动到非法位置（自身子树内）报错', () => {
  const store = new OutlineStore([
    { id: 'c1', title: '第一章', level: 1 },
    { id: 's1', title: '第一节', level: 2 },
  ]);
  assert.throws(() => store.applyEdit({ kind: 'move', id: 'c1', to: { beforeId: 's1' } }));
  assert.equal(store.version, 1);
});
