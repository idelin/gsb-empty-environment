import assert from 'node:assert/strict';
import test from 'node:test';
import { Store } from '../src/store.ts';
import { serializeStore, stableStringify, type SnapshotFile } from '../src/serialize.ts';
import { makeRegistry, makeStore } from './helpers.ts';

function roundTrip(file: SnapshotFile): SnapshotFile {
  return JSON.parse(JSON.stringify(file)) as SnapshotFile;
}

test('序列化-恢复往返：值与版本语义保持一致', () => {
  const registry = makeRegistry();
  const store = makeStore(registry);
  store.begin().set('a', 10).commit();

  const restored = new Store(registry);
  restored.restore(roundTrip(serializeStore(store)));

  assert.equal(restored.get('a'), 10);
  assert.equal(restored.get('b'), 2);
  assert.equal(restored.get('sum'), 12);
  assert.equal(restored.get('product'), 20);
  assert.equal(restored.get('total'), 11); // 单调节点携带值，不重复累积
});

test('函数绝不直接序列化：快照中没有任何函数体', () => {
  const file = serializeStore(makeStore());
  const text = JSON.stringify(file);
  assert.ok(!text.includes('"compute"'), '快照不应包含 compute 字段');
  assert.ok(!text.includes('=>'), '快照不应包含函数源码');
  assert.ok(!text.includes('function'), '快照不应包含函数源码');
  // 计算节点只保存定义标识 + 版本 + 依赖边
  const sum = file.nodes.find((n) => n.id === 'sum');
  assert.deepEqual(sum && 'def' in sum ? sum.def : null, { id: 'add', version: 1 });
  assert.deepEqual(sum && 'deps' in sum ? sum.deps : null, ['a', 'b']);
});

test('共享实体恢复后仍共享：一个 set 同时驱动两个依赖者', () => {
  const registry = makeRegistry();
  const store = makeStore(registry);
  const restored = new Store(registry);
  restored.restore(roundTrip(serializeStore(store)));

  // 'a' 在恢复后的图中只有一个节点记录
  const ids = [...restored.recordsView().keys()].filter((id) => id === 'a');
  assert.equal(ids.length, 1);

  const seen: string[] = [];
  restored.subscribe('sum', (v) => seen.push(`sum=${v}`));
  restored.subscribe('product', (v) => seen.push(`product=${v}`));
  restored.begin().set('a', 3).commit();
  assert.deepEqual(seen.sort(), ['product=6', 'sum=5']);
});

test('恢复后可继续正常事务与订阅', () => {
  const registry = makeRegistry();
  const restored = new Store(registry);
  restored.restore(roundTrip(serializeStore(makeStore(registry))));
  restored.begin().set('b', 100).commit();
  assert.equal(restored.get('sum'), 101);
});

test('stableStringify 与键序无关', () => {
  const a = stableStringify({ x: 1, y: [2, { b: 1, a: 2 }] });
  const b = stableStringify({ y: [2, { a: 2, b: 1 }], x: 1 });
  assert.equal(a, b);
});

test('恢复后操作日志可继续回放（含 restore 批次）', () => {
  const registry = makeRegistry();
  const original = makeStore(registry);
  original.begin().set('a', 5).commit();

  const restored = new Store(registry);
  restored.restore(roundTrip(serializeStore(original)));
  restored.begin().set('b', 9).commit();

  const replayed = Store.replay(restored.ops(), registry);
  assert.equal(replayed.get('a'), 5);
  assert.equal(replayed.get('b'), 9);
  assert.equal(replayed.get('sum'), 14);
  assert.equal(replayed.get('total'), restored.get('total'));
});
