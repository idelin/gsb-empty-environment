import assert from 'node:assert/strict';
import test from 'node:test';
import {
  AmbiguousTitleError,
  CyclicMoveError,
  LevelSkipError,
  NumberedDocument,
} from '../src';

function makeDoc(): NumberedDocument {
  return new NumberedDocument([
    { id: 'intro', title: '引言', level: 1 },
    { id: 'bg', title: '背景', level: 2 },
    { id: 'bg-detail', title: '背景细节', level: 3 },
    { id: 'ch2', title: '第二章', level: 1 },
    { id: 'sec21', title: '方法', level: 2 },
  ]);
}

function numbersOf(doc: NumberedDocument): Record<string, string> {
  return Object.fromEntries(doc.getOutline().map((o) => [o.id, o.number]));
}

test('初始编号按树结构生成', () => {
  const doc = makeDoc();
  assert.deepEqual(numbersOf(doc), {
    intro: '1',
    bg: '1.1',
    'bg-detail': '1.1.1',
    ch2: '2',
    sec21: '2.1',
  });
});

test('重命名不改编号，引用显示新标题并标记 stale', () => {
  const doc = makeDoc();
  const ref = doc.addReference('bg');
  const result = doc.rename('bg', '研究背景');
  assert.equal(result.ok, true);
  assert.deepEqual(result.ok ? result.changes : null, []);
  assert.equal(doc.getNumber('bg'), '1.1');
  const resolved = doc.resolveReference(ref.id);
  assert.equal(resolved.status, 'ok');
  assert.equal(resolved.stale, true);
  assert.equal(resolved.display, '1.1 研究背景');
  assert.equal(resolved.snapshotDisplay, '1.1 背景');
});

test('删除父标题：子标题上移一级，引用父标题悬空、引用子标题仍有效', () => {
  const doc = makeDoc();
  const refParent = doc.addReference('bg');
  const refChild = doc.addReference('bg-detail');

  const result = doc.remove('bg');
  assert.equal(result.ok, true);
  if (!result.ok) return;

  assert.deepEqual(
    result.changes.map((c) => [c.id, c.oldNumber, c.newNumber]),
    [
      ['bg', '1.1', null],
      ['bg-detail', '1.1.1', '1.1'],
    ],
  );
  assert.deepEqual(numbersOf(doc), {
    intro: '1',
    'bg-detail': '1.1',
    ch2: '2',
    sec21: '2.1',
  });

  const dangling = doc.resolveReference(refParent.id);
  assert.equal(dangling.status, 'dangling');
  assert.equal(dangling.current, null);
  assert.equal(dangling.display, '1.1 背景 (已失效)');
  assert.deepEqual(result.danglingReferences.map((r) => r.reference.id), [refParent.id]);

  const child = doc.resolveReference(refChild.id);
  assert.equal(child.status, 'ok');
  assert.equal(child.display, '1.1 背景细节');
  assert.equal(child.stale, true);
});

test('级联删除父标题：整棵子树删除，两个引用都悬空', () => {
  const doc = makeDoc();
  const refParent = doc.addReference('bg');
  const refChild = doc.addReference('bg-detail');
  const result = doc.remove('bg', { cascade: true });
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.deepEqual(
    result.changes.map((c) => c.id),
    ['bg', 'bg-detail'],
  );
  assert.deepEqual(numbersOf(doc), { intro: '1', ch2: '2', sec21: '2.1' });
  assert.equal(doc.resolveReference(refParent.id).status, 'dangling');
  assert.equal(doc.resolveReference(refChild.id).status, 'dangling');
});

test('同名标题：引用绑定稳定 ID 互不干扰，按标题查找报告歧义', () => {
  const doc = new NumberedDocument([
    { id: 'ch1', title: '第一章', level: 1 },
    { id: 'sum1', title: '概述', level: 2 },
    { id: 'ch2', title: '第二章', level: 1 },
    { id: 'sum2', title: '概述', level: 2 },
  ]);
  const ref1 = doc.addReference('sum1');
  const ref2 = doc.addReference('sum2');

  assert.deepEqual(
    doc.findByTitle('概述').map((h) => h.id),
    ['sum1', 'sum2'],
  );
  assert.throws(() => doc.getByTitle('概述'), AmbiguousTitleError);

  // 跨章节移动其中一个同名标题，两个引用各自跟踪自己的目标
  const moved = doc.move('sum2', 'ch1', 1);
  assert.equal(moved.ok, true);
  assert.equal(doc.getNumber('sum2'), '1.2');
  assert.equal(doc.getNumber('sum1'), '1.1');
  assert.equal(doc.resolveReference(ref1.id).display, '1.1 概述');
  assert.equal(doc.resolveReference(ref2.id).display, '1.2 概述');
  assert.equal(doc.resolveReference(ref1.id).stale, false);
  assert.equal(doc.resolveReference(ref2.id).stale, true);
});

test('跨章节移动：子树编号随树重排，后续章节增量重编号，引用保持有效', () => {
  const doc = makeDoc();
  const ref = doc.addReference('sec21');
  const before = doc.version;

  const result = doc.move('sec21', 'intro', 1);
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.ok(result.version > before);

  assert.deepEqual(numbersOf(doc), {
    intro: '1',
    bg: '1.1',
    'bg-detail': '1.1.1',
    sec21: '1.2',
    ch2: '2',
  });
  // 增量输出：只有编号真正变化的标题出现在 changes 里
  assert.deepEqual(
    result.changes.map((c) => [c.id, c.oldNumber, c.newNumber]),
    [['sec21', '2.1', '1.2']],
  );

  const resolved = doc.resolveReference(ref.id);
  assert.equal(resolved.status, 'ok');
  assert.equal(resolved.display, '1.2 方法');
  assert.equal(resolved.snapshotDisplay, '2.1 方法');
  assert.equal(resolved.stale, true);
});

test('移动到顶层：层级自动平移，受影响章节重编号', () => {
  const doc = makeDoc();
  const result = doc.move('bg-detail', null);
  assert.equal(result.ok, true);
  // 缺省追加到顶层末尾
  assert.deepEqual(numbersOf(doc), {
    intro: '1',
    bg: '1.1',
    ch2: '2',
    sec21: '2.1',
    'bg-detail': '3',
  });
  // 指定 childIndex 可插入到任意顶层位置
  const result2 = doc.move('bg-detail', null, 1);
  assert.equal(result2.ok, true);
  assert.deepEqual(numbersOf(doc), {
    intro: '1',
    'bg-detail': '2',
    ch2: '3',
    sec21: '3.1',
    bg: '1.1',
  });
});

test('旧版本引用显示：快照保留创建时编号，编辑后可对比当前显示', () => {
  const doc = makeDoc();
  const ref = doc.addReference('bg');
  assert.equal(ref.createdVersion, 0);
  assert.equal(ref.snapshotNumber, '1.1');

  doc.insert(0, { id: 'preface', title: '前言', level: 1 });
  doc.rename('bg', '研究背景');

  const resolved = doc.resolveReference(ref.id);
  assert.equal(resolved.status, 'ok');
  assert.equal(resolved.stale, true);
  assert.deepEqual(resolved.current, { number: '2.1', title: '研究背景' });
  assert.equal(resolved.display, '2.1 研究背景');
  assert.equal(resolved.snapshotDisplay, '1.1 背景');

  doc.remove('bg');
  const afterDelete = doc.resolveReference(ref.id);
  assert.equal(afterDelete.status, 'dangling');
  assert.equal(afterDelete.display, '1.1 背景 (已失效)');
});

test('禁止跳级：插入跳级标题被拒绝并给出最小修复范围，正文不变', () => {
  const doc = makeDoc();
  const versionBefore = doc.version;
  const outlineBefore = doc.getOutline();

  // 在 intro(1) 之后直接插入 level 3 的标题 → 跳级
  const result = doc.insert(1, { id: 'bad', title: '跳级', level: 3 });
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.ok(result.error instanceof LevelSkipError);
  const err = result.error as LevelSkipError;
  assert.deepEqual(err.skips, [{ headingId: 'bad', maxAllowedLevel: 2, actualLevel: 3 }]);
  // 最小修复：只需把 bad 自己降一级（后续 bg/bg-detail 原本就在 intro 之下）
  assert.equal(err.fix.direction, 'lower-following');
  assert.deepEqual(err.fix.adjustments, [{ id: 'bad', delta: -1 }]);

  // 文档未被擅自修改
  assert.equal(doc.version, versionBefore);
  assert.deepEqual(doc.getOutline(), outlineBefore);
});

test('禁止跳级：改级别造成跳级时回滚，修复范围取两侧较小者', () => {
  const doc = new NumberedDocument([
    { id: 'a', title: 'A', level: 1 },
    { id: 'b', title: 'B', level: 1 },
    { id: 'c', title: 'C', level: 2 },
    { id: 'd', title: 'D', level: 1 },
  ]);
  const result = doc.setLevel('c', 4);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.ok(result.error instanceof LevelSkipError);
  const err = result.error as LevelSkipError;
  assert.equal(err.skips[0].headingId, 'c');
  assert.equal(err.skips[0].maxAllowedLevel, 2);
  // 最小修复：只需把 c 自己降 2 级，不涉及其它标题
  assert.equal(err.fix.direction, 'lower-following');
  assert.deepEqual(err.fix.adjustments, [{ id: 'c', delta: -2 }]);
  // 回滚：层级与编号保持原样
  assert.deepEqual(numbersOf(doc), { a: '1', b: '2', c: '2.1', d: '3' });
});

test('改级别连带子树平移造成跳级时，修复范围覆盖整个子树', () => {
  const doc = new NumberedDocument([
    { id: 'a', title: 'A', level: 1 },
    { id: 'b', title: 'B', level: 1 },
    { id: 'c', title: 'C', level: 2 },
    { id: 'd', title: 'D', level: 3 },
  ]);
  const result = doc.setLevel('c', 4);
  assert.equal(result.ok, false);
  if (result.ok) return;
  const err = result.error as LevelSkipError;
  // c 的子树被整体抬到 4/5 级，最小修复是 c、d 各降 2 级
  assert.deepEqual(err.fix.adjustments, [
    { id: 'c', delta: -2 },
    { id: 'd', delta: -2 },
  ]);
  assert.deepEqual(numbersOf(doc), { a: '1', b: '2', c: '2.1', d: '2.1.1' });
});

test('移动到自身子树内被拒绝', () => {
  const doc = makeDoc();
  const result = doc.move('bg', 'bg-detail');
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.ok(result.error instanceof CyclicMoveError);
  assert.equal(doc.getNumber('bg'), '1.1');
});

test('增量编号：中间插入只输出受影响的编号变化', () => {
  const doc = makeDoc();
  const result = doc.insert(2, { id: 'new-sec', title: '新节', level: 2 });
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.deepEqual(
    result.changes.map((c) => [c.id, c.oldNumber, c.newNumber]),
    [
      ['new-sec', null, '1.2'],
      ['bg-detail', '1.1.1', '1.2.1'],
    ],
  );
  // 未受影响的章节编号不变
  assert.equal(doc.getNumber('ch2'), '2');
  assert.equal(doc.getNumber('sec21'), '2.1');
});

test('构造时跳级直接抛错', () => {
  assert.throws(
    () => new NumberedDocument([{ id: 'x', title: 'X', level: 2 }]),
    LevelSkipError,
  );
});
