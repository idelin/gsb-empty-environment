import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatReferenceDisplay, OutlineStore } from '../src/index.ts';

function store() {
  return new OutlineStore([
    { id: 'c1', title: '第一章', level: 1 },
    { id: 's1', title: '背景', level: 2 },
    { id: 'c2', title: '第二章', level: 1 },
  ]);
}

test('旧版本引用显示：钉住 v1，移动后同时展示旧编号与当前编号', () => {
  const s = store();
  s.addReference({ id: 'r', targetId: 's1', pinnedVersion: 1 });

  s.applyEdit({ kind: 'move', id: 's1', to: { afterId: 'c2' } });
  s.applyEdit({ kind: 'rename', id: 's1', title: '背景介绍' });

  const resolved = s.resolveReference('r');
  assert.ok(resolved.status === 'ok');
  assert.equal(resolved.number, '2.1');
  assert.equal(resolved.title, '背景介绍');
  assert.deepEqual(resolved.pinned, { version: 1, number: '1.1', title: '背景' });
  assert.equal(resolved.changedSincePinned, true);
  assert.equal(
    formatReferenceDisplay(resolved),
    '§1.1 背景（v1，当前为 §2.1 背景介绍）',
  );
});

test('旧版本引用显示：内容未变时不附加当前信息', () => {
  const s = store();
  s.addReference({ id: 'r', targetId: 'c1', pinnedVersion: 1 });
  s.applyEdit({ kind: 'rename', id: 'c2', title: '第二章（修订）' });
  const resolved = s.resolveReference('r');
  assert.ok(resolved.status === 'ok');
  assert.equal(resolved.changedSincePinned, false);
  assert.equal(formatReferenceDisplay(resolved), '§1 第一章');
});

test('旧版本引用显示：目标已删除时展示钉住版本的快照', () => {
  const s = store();
  s.addReference({ id: 'r', targetId: 's1', pinnedVersion: 1 });
  s.applyEdit({ kind: 'remove', id: 's1' });
  const resolved = s.resolveReference('r');
  assert.ok(resolved.status === 'dangling');
  assert.deepEqual(resolved.lastKnown, { version: 1, number: '1.1', title: '背景' });
  assert.equal(formatReferenceDisplay(resolved), '§1.1 背景（引用已失效，最后见于 v1）');
});

test('悬空引用：未钉住版本时回退到最后出现的版本', () => {
  const s = store();
  s.applyEdit({ kind: 'rename', id: 's1', title: '背景 v2' });
  s.addReference({ id: 'r', targetId: 's1' });
  s.applyEdit({ kind: 'remove', id: 's1' });
  const resolved = s.resolveReference('r');
  assert.ok(resolved.status === 'dangling');
  assert.deepEqual(resolved.lastKnown, { version: 2, number: '1.1', title: '背景 v2' });
});

test('悬空引用：目标从未存在时 lastKnown 为 null', () => {
  const s = store();
  s.addReference({ id: 'r', targetId: 'ghost' });
  const resolved = s.resolveReference('r');
  assert.ok(resolved.status === 'dangling');
  assert.equal(resolved.lastKnown, null);
  assert.equal(formatReferenceDisplay(resolved), '§? ghost（引用已失效）');
});

test('钉住未来版本报错', () => {
  const s = store();
  assert.throws(() => s.addReference({ id: 'r', targetId: 'c1', pinnedVersion: 99 }));
});
