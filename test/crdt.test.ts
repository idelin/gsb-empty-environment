import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JsonCRDT } from '../src/document.ts';
import type { JsonObject } from '../src/nodes.ts';

const initial = {
  meta: { version: 1, name: 'demo' },
  list: [
    { title: 'hello', tags: ['ts'], done: false },
    { title: 'world', tags: [], done: true },
  ],
} satisfies JsonObject;

test('验收：并发修改 root.list[0].title 与 root.list[0].tags.push 精准合并，且不替换列表项引用', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');

  // 两个副本并发修改同一列表项的不同层级属性
  a.set(['list', 0, 'title'], 'hello v2');
  b.push(['list', 0, 'tags'], 'go');

  // 合并前捕获 A 侧 list[0] 的内存对象引用
  const itemRefBefore = a.nodeAt(['list', 0]);
  const tagsRefBefore = a.nodeAt(['list', 0, 'tags']);

  a.merge(b);

  // 两个并发修改均被保留
  assert.equal(a.get(['list', 0, 'title']), 'hello v2');
  assert.deepEqual(a.get(['list', 0, 'tags']), ['ts', 'go']);

  // 列表项及其 tags 子树的内存对象引用未被覆盖
  assert.strictEqual(a.nodeAt(['list', 0]), itemRefBefore);
  assert.strictEqual(a.nodeAt(['list', 0, 'tags']), tagsRefBefore);

  // 未触碰的兄弟属性与兄弟元素不受影响
  assert.equal(a.get(['list', 0, 'done']), false);
  assert.equal(a.get(['list', 1, 'title']), 'world');
});

test('收敛：双向合并结果一致（交换律）且重复合并幂等', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');

  a.set(['list', 0, 'title'], 'from A');
  a.set(['meta', 'name'], 'renamed');
  b.push(['list', 0, 'tags'], 'go');
  b.push(['list', 1, 'tags'], 'x');

  a.merge(b);
  b.merge(a);

  assert.deepEqual(a.value(), b.value());

  const snapshot = JSON.stringify(a.value());
  a.merge(b); // 幂等
  b.merge(a);
  assert.equal(JSON.stringify(a.value()), snapshot);
  assert.equal(JSON.stringify(b.value()), snapshot);
});

test('并发 push 同一数组：两个元素都保留且各副本定序一致', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');

  a.push(['list', 0, 'tags'], 'a-tag');
  b.push(['list', 0, 'tags'], 'b-tag');

  a.merge(b);
  b.merge(a);

  const tagsA = a.get(['list', 0, 'tags']) as string[];
  const tagsB = b.get(['list', 0, 'tags']) as string[];
  assert.deepEqual(tagsA, tagsB);
  assert.deepEqual([...tagsA].sort(), ['a-tag', 'b-tag', 'ts']);
  assert.equal(tagsA[0], 'ts'); // 原元素仍在首位
});

test('混合嵌套：Map/Array/Register 跨层级并发修改互不干扰', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');
  const c = a.fork('C');

  a.set(['meta', 'version'], 2); // 顶层 Map 内 Register
  b.push(['list'], { title: 'third', tags: ['new'], done: false }); // 顶层数组追加对象
  c.set(['list', 1, 'done'], false); // 数组元素内 Register
  a.set(['list', 0, 'tags', 0], 'typescript'); // 数组元素内数组元素

  a.merge(b).merge(c);
  b.merge(a).merge(c);
  c.merge(a).merge(b);

  const expected = {
    meta: { version: 2, name: 'demo' },
    list: [
      { title: 'hello', tags: ['typescript'], done: false },
      { title: 'world', tags: [], done: false },
      { title: 'third', tags: ['new'], done: false },
    ],
  };
  assert.deepEqual(a.value(), expected);
  assert.deepEqual(a.value(), b.value());
  assert.deepEqual(b.value(), c.value());
});

test('删除语义：Map 键删除与数组元素删除在合并后生效', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');

  a.remove(['meta', 'name']);
  b.remove(['list', 1]);
  b.set(['list', 0, 'title'], 'kept');

  a.merge(b);
  b.merge(a);

  assert.equal(a.get(['meta', 'name']), undefined);
  assert.equal((a.get(['list']) as unknown[]).length, 1);
  assert.equal(a.get(['list', 0, 'title']), 'kept');
  assert.deepEqual(a.value(), b.value());
});

test('并发写同一 Register：按 Lamport 时钟 LWW 裁决且全副本一致', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');

  a.set(['list', 0, 'title'], 'A wins?');
  b.set(['list', 0, 'title'], 'B wins?');

  a.merge(b);
  b.merge(a);

  assert.equal(a.get(['list', 0, 'title']), b.get(['list', 0, 'title']));
  assert.ok(['A wins?', 'B wins?'].includes(a.get(['list', 0, 'title']) as string));
});

test('数组中间插入：并发插入不丢失且定序确定', () => {
  const a = JsonCRDT.create('A', { nums: [1, 4] });
  const b = a.fork('B');

  a.insert(['nums'], 1, 2);
  b.insert(['nums'], 1, 3);

  a.merge(b);
  b.merge(a);

  const numsA = a.get(['nums']) as number[];
  const numsB = b.get(['nums']) as number[];
  assert.deepEqual(numsA, numsB);
  assert.deepEqual([...numsA].sort((x, y) => x - y), [1, 2, 3, 4]);
  assert.equal(numsA[0], 1);
  assert.equal(numsA[numsA.length - 1], 4);
});

test('fork 后互不影响：副本状态相互独立', () => {
  const a = JsonCRDT.create('A', initial);
  const b = a.fork('B');

  b.set(['list', 0, 'title'], 'only in B');
  assert.equal(a.get(['list', 0, 'title']), 'hello');
  assert.equal(b.get(['list', 0, 'title']), 'only in B');
});
