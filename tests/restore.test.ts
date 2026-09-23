import assert from 'node:assert/strict';
import test from 'node:test';
import { RestoreError } from '../src/errors.ts';
import { Store } from '../src/store.ts';
import { serializeStore, type SnapshotFile } from '../src/serialize.ts';
import { makeRegistry, makeStore } from './helpers.ts';

function validFile(): SnapshotFile {
  return JSON.parse(JSON.stringify(serializeStore(makeStore()))) as SnapshotFile;
}

function expectRestoreError(store: Store, file: unknown, code: string): void {
  assert.throws(
    () => store.restore(file),
    (e: unknown) => e instanceof RestoreError && e.code === code,
    `expected RestoreError(${code})`,
  );
}

test('schema 版本不兼容：拒绝恢复', () => {
  const store = makeStore();
  expectRestoreError(store, { ...validFile(), schemaVersion: 999 }, 'SCHEMA_VERSION');
  expectRestoreError(store, { ...validFile(), schemaVersion: 0 }, 'SCHEMA_VERSION');
  expectRestoreError(store, null, 'SCHEMA_VERSION');
});

test('未知计算定义：拒绝恢复', () => {
  const store = makeStore();
  const file = validFile();
  for (const n of file.nodes) {
    if (n.kind === 'computed' && n.id === 'sum') {
      (n.def as { id: string }).id = 'definitely-unknown';
    }
  }
  expectRestoreError(store, file, 'UNKNOWN_DEFINITION');
});

test('定义版本不符：拒绝恢复', () => {
  const store = makeStore();
  const file = validFile();
  for (const n of file.nodes) {
    if (n.kind === 'computed') (n.def as { version: number }).version = 99;
  }
  expectRestoreError(store, file, 'DEFINITION_VERSION');
});

test('缺失依赖节点：拒绝恢复', () => {
  const store = makeStore();
  const file = validFile();
  // 删掉状态节点 b，sum/product 的依赖悬空
  (file.nodes as unknown[]).splice(
    file.nodes.findIndex((n) => n.id === 'b'),
    1,
  );
  expectRestoreError(store, file, 'MISSING_NODE');
});

test('依赖边指向不存在的节点：拒绝恢复', () => {
  const store = makeStore();
  const file = validFile();
  for (const n of file.nodes) {
    if (n.kind === 'computed' && n.id === 'sum') (n.deps as string[]).push('ghost');
  }
  expectRestoreError(store, file, 'MISSING_NODE');
});

test('非法求值环：拒绝恢复（与单调环分开检查）', () => {
  const store = makeStore();
  const file: SnapshotFile = {
    schemaVersion: 1,
    nodes: [
      { id: 'a', kind: 'state', value: 1 },
      { id: 'x', kind: 'computed', def: { id: 'add', version: 1 }, deps: ['y', 'a'] },
      { id: 'y', kind: 'computed', def: { id: 'add', version: 1 }, deps: ['x', 'a'] },
    ],
  };
  expectRestoreError(store, file, 'ILLEGAL_CYCLE');
});

test('非单调定义上的自反馈边：按单调环违规拒绝', () => {
  const store = makeStore();
  const file: SnapshotFile = {
    schemaVersion: 1,
    nodes: [
      { id: 'a', kind: 'state', value: 1 },
      { id: 'x', kind: 'computed', def: { id: 'add', version: 1 }, deps: ['x', 'a'] },
    ],
  };
  expectRestoreError(store, file, 'MONOTONIC_CYCLE');
});

test('合法单调环：允许恢复并保持携带值', () => {
  const store = makeStore();
  const file: SnapshotFile = {
    schemaVersion: 1,
    nodes: [
      { id: 'a', kind: 'state', value: 5 },
      { id: 't', kind: 'computed', def: { id: 'acc', version: 1 }, deps: ['t', 'a'], carried: 42 },
    ],
  };
  store.restore(file);
  assert.equal(store.get('t'), 42);
  store.begin().set('a', 8).commit();
  assert.equal(store.get('t'), 50);
});

test('重复节点 id：违反共享身份，拒绝恢复', () => {
  const store = makeStore();
  const file = validFile();
  (file.nodes as unknown[]).push({ id: 'a', kind: 'state', value: 999 });
  expectRestoreError(store, file, 'DUPLICATE_NODE');
});

test('恢复期间订阅回调不提前运行：完整恢复后只触发一次', () => {
  const registry = makeRegistry();
  const store = makeStore(registry);
  const calls: unknown[] = [];
  store.subscribe('sum', (v) => calls.push(v));

  const file = validFile();
  for (const n of file.nodes) {
    if (n.id === 'a' && n.kind === 'state') (n as { value: unknown }).value = 100;
  }
  store.restore(file);
  assert.deepEqual(calls, [102]); // 只在完整发布后触发一次，值为最终值
});

test('失败的恢复不触发任何订阅回调', () => {
  const store = makeStore();
  const calls: unknown[] = [];
  store.subscribe('sum', (v) => calls.push(v));
  store.subscribe('a', (v) => calls.push(v));
  expectRestoreError(store, { ...validFile(), schemaVersion: 999 }, 'SCHEMA_VERSION');
  assert.deepEqual(calls, []);
});

test('恢复失败后旧存活状态保持可读、可继续提交', () => {
  const store = makeStore();
  store.begin().set('a', 7).commit();
  const versionBefore = store.version;

  const bad = validFile();
  for (const n of bad.nodes) {
    if (n.kind === 'computed') (n.def as { version: number }).version = 42;
  }
  expectRestoreError(store, bad, 'DEFINITION_VERSION');

  assert.equal(store.get('a'), 7);
  assert.equal(store.get('sum'), 9);
  assert.equal(store.version, versionBefore);

  store.begin().set('a', 8).commit();
  assert.equal(store.get('sum'), 10);
});

test('恢复被移除节点的订阅者收到 undefined', () => {
  const store = makeStore();
  const calls: unknown[] = [];
  store.subscribe('product', (v) => calls.push(v));
  const file: SnapshotFile = {
    schemaVersion: 1,
    nodes: [{ id: 'a', kind: 'state', value: 1 }],
  };
  store.restore(file);
  assert.deepEqual(calls, [undefined]);
  assert.equal(store.has('product'), false);
});
