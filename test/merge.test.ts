import test from 'node:test';
import assert from 'node:assert/strict';
import { merge3 } from '../src/merge.ts';
import { applyPatch } from '../src/patch.ts';
import { deepClone } from '../src/types.ts';

test('一侧删除另一侧修改 → 冲突并携带三方值', () => {
  const base = { a: { x: 1 }, keep: true };
  const left = { keep: true }; // 删除 a
  const right = { a: { x: 2 }, keep: true }; // 修改 a.x

  const m = merge3(base, left, right);
  assert.equal(m.conflicts.length, 1);
  const c = m.conflicts[0];
  assert.deepEqual(c.path, ['a']);
  assert.equal(c.reason, 'delete-vs-modify');
  assert.deepEqual(c.base, { x: 1 });
  assert.equal(c.left, undefined, '删除侧应为 absent');
  assert.deepEqual(c.right, { x: 2 });
});

test('数组移动同时属性更正应保留身份（稳定ID模式）', () => {
  const base = { items: [{ id: 'a', v: 1 }, { id: 'b', v: 2 }, { id: 'c', v: 3 }] };
  const left = { items: [{ id: 'c', v: 3 }, { id: 'a', v: 1 }, { id: 'b', v: 2 }] }; // c 移到首位
  const right = { items: [{ id: 'a', v: 1 }, { id: 'b', v: 20 }, { id: 'c', v: 3 }] }; // 更正 b.v

  const m = merge3(base, left, right, { arrayMode: 'id' });
  assert.deepEqual(m.conflicts, []);
  assert.deepEqual(m.result, { items: [{ id: 'c', v: 3 }, { id: 'a', v: 1 }, { id: 'b', v: 20 }] });

  // 身份保留：补丁是 move + 属性 set，没有对 b 的 remove/insert。
  const itemOps = m.patch.ops.filter((o) => (o.op === 'move' ? false : String(o.path[0]) === 'items'));
  assert.ok(m.patch.ops.some((o) => o.op === 'move'), '应包含 move 操作');
  assert.ok(!itemOps.some((o) => o.op === 'remove' || o.op === 'insert'), '不应删除/插入既有元素');
  assert.ok(
    m.patch.ops.some((o) => o.op === 'set' && o.path.join(',') === 'items,2,v' && o.value === 20),
    '属性更正应落在移动后的下标',
  );

  // 来源映射：b 元素记录为 moved，来源是基线旧下标 1。
  const moved = m.sources.find((s) => s.relation === 'moved' && s.outPath.join(',') === 'items,2');
  assert.ok(moved, '应记录移动来源');
  assert.deepEqual(moved.path, ['items', 1]);
  assert.equal(moved.side, 'base');

  // 补丁应用到基线 == 合并结果。
  assert.deepEqual(applyPatch(deepClone(base), m.patch), m.result);
});

test('复制节点不被误作移动', () => {
  const block = { id: 'B', data: [1, 2] };
  const base = { block, name: 'root' };
  const left = { block: deepClone(block), copy: deepClone(block), name: 'root' }; // 复制 block 到 copy
  const right = deepClone(base);

  const m = merge3(base, left, right, { arrayMode: 'id' });
  assert.deepEqual(m.conflicts, []);
  assert.ok(!m.patch.ops.some((o) => o.op === 'move'), '复制不应产生 move 操作');
  assert.ok(m.patch.ops.some((o) => o.op === 'set' && o.path.join(',') === 'copy'));

  const src = m.sources.find((s) => s.outPath.join(',') === 'copy');
  assert.ok(src, '应有 copy 的来源记录');
  assert.equal(src.relation, 'copied', '与基线既有子树同内容 → copied 而非 moved');
  assert.equal(src.side, 'left');
});
