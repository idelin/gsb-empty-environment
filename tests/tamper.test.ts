import assert from 'node:assert/strict';
import test from 'node:test';
import { RestoreError } from '../src/errors.ts';
import { Store } from '../src/store.ts';
import { serializeStore, type SnapshotFile } from '../src/serialize.ts';
import { makeRegistry, makeStore } from './helpers.ts';

/** 生成带完整性摘要的快照，返回其深副本。 */
function sealedFile(): SnapshotFile {
  return JSON.parse(JSON.stringify(serializeStore(makeStore(), { integrity: true }))) as SnapshotFile;
}

function expectReject(store: Store, file: unknown, code: string): void {
  assert.throws(
    () => store.restore(file),
    (e: unknown) => e instanceof RestoreError && e.code === code,
    `expected RestoreError(${code})`,
  );
}

function assertLiveStateIntact(store: Store): void {
  assert.equal(store.get('a'), 1);
  assert.equal(store.get('b'), 2);
  assert.equal(store.get('sum'), 3);
  assert.equal(store.get('product'), 2);
  assert.equal(store.get('total'), 1);
}

test('篡改状态节点值：完整性校验拒绝，旧状态可读', () => {
  const store = makeStore();
  const file = sealedFile();
  for (const n of file.nodes) {
    if (n.id === 'a') (n as { value: unknown }).value = 9999;
  }
  expectReject(store, file, 'INTEGRITY');
  assertLiveStateIntact(store);
});

test('篡改节点 id：完整性校验拒绝', () => {
  const store = makeStore();
  const file = sealedFile();
  file.nodes[0]!.id = 'forged';
  expectReject(store, file, 'INTEGRITY');
  assertLiveStateIntact(store);
});

test('篡改计算定义版本：完整性校验拒绝', () => {
  const store = makeStore();
  const file = sealedFile();
  for (const n of file.nodes) {
    if (n.kind === 'computed') (n.def as { version: number }).version = 2;
  }
  expectReject(store, file, 'INTEGRITY');
  assertLiveStateIntact(store);
});

test('篡改依赖边：完整性校验拒绝', () => {
  const store = makeStore();
  const file = sealedFile();
  for (const n of file.nodes) {
    if (n.id === 'sum') (n.deps as string[])[0] = 'b';
  }
  expectReject(store, file, 'INTEGRITY');
  assertLiveStateIntact(store);
});

test('删除完整性字段后篡改：结构性校验依然拒绝', () => {
  const registry = makeRegistry();

  // 篡改版本（无完整性字段）→ 定义版本校验
  {
    const store = makeStore(registry);
    const file = JSON.parse(JSON.stringify(serializeStore(makeStore(registry)))) as SnapshotFile;
    for (const n of file.nodes) {
      if (n.kind === 'computed') (n.def as { version: number }).version = 77;
    }
    expectReject(store, file, 'DEFINITION_VERSION');
    assertLiveStateIntact(store);
  }

  // 篡改依赖边指向不存在节点 → 缺节点校验
  {
    const store = makeStore(registry);
    const file = JSON.parse(JSON.stringify(serializeStore(makeStore(registry)))) as SnapshotFile;
    for (const n of file.nodes) {
      if (n.id === 'sum') (n.deps as string[]).push('ghost');
    }
    expectReject(store, file, 'MISSING_NODE');
    assertLiveStateIntact(store);
  }

  // 篡改依赖边制造求值环 → 环校验
  {
    const store = makeStore(registry);
    const file = JSON.parse(JSON.stringify(serializeStore(makeStore(registry)))) as SnapshotFile;
    for (const n of file.nodes) {
      if (n.id === 'sum') (n.deps as string[]).push('product');
      if (n.id === 'product') (n.deps as string[]).push('sum');
    }
    expectReject(store, file, 'ILLEGAL_CYCLE');
    assertLiveStateIntact(store);
  }

  // 删除被依赖的节点 → 缺节点校验
  {
    const store = makeStore(registry);
    const file = JSON.parse(JSON.stringify(serializeStore(makeStore(registry)))) as SnapshotFile;
    (file.nodes as unknown[]).splice(
      file.nodes.findIndex((n) => n.id === 'b'),
      1,
    );
    expectReject(store, file, 'MISSING_NODE');
    assertLiveStateIntact(store);
  }
});

test('篡改未知计算定义标识：拒绝且旧状态可读', () => {
  const store = makeStore();
  const file = JSON.parse(JSON.stringify(serializeStore(makeStore()))) as SnapshotFile;
  for (const n of file.nodes) {
    if (n.kind === 'computed') (n.def as { id: string }).id = 'evil-definition';
  }
  expectReject(store, file, 'UNKNOWN_DEFINITION');
  assertLiveStateIntact(store);
});

test('多次失败恢复后 store 仍可正常工作', () => {
  const store = makeStore();
  for (let i = 0; i < 3; i++) {
    const file = sealedFile();
    (file.nodes[0] as { value?: unknown }).value = 1000 + i;
    expectReject(store, file, 'INTEGRITY');
  }
  assertLiveStateIntact(store);
  store.begin().set('a', 5).commit();
  assert.equal(store.get('sum'), 7);
});
