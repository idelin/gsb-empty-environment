import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Replica } from '../src/replica.ts';
import { VersionVector } from '../src/version-vector.ts';

test('读取不存在的键返回空上下文', () => {
  const r1 = new Replica('R1');
  const res = r1.get('k');
  assert.equal(res.siblings.length, 0);
  assert.ok(res.context.isEmpty());
});

test('首次写入产生 (R1:1)，携带上下文的覆盖写入产生 (R1:2)', () => {
  const r1 = new Replica('R1');
  const first = r1.put('k', 'v1', VersionVector.empty());
  assert.equal(first.outcome, 'fast-overwrite');
  assert.equal(first.version.toString(), '{R1:1}');

  const read = r1.get('k');
  assert.equal(read.siblings.length, 1);
  assert.equal(read.siblings[0].value, 'v1');
  assert.equal(read.context.toString(), '{R1:1}');

  const second = r1.put('k', 'v2', read.context);
  assert.equal(second.outcome, 'fast-overwrite');
  assert.equal(second.version.toString(), '{R1:2}');
  assert.equal(r1.get('k').siblings.length, 1);
  assert.equal(r1.get('k').siblings[0].value, 'v2');
});

test('过期上下文的写入产生 Sibling 分叉', () => {
  const r1 = new Replica('R1');
  r1.put('k', 'v1', VersionVector.empty());
  r1.put('k', 'v2', r1.get('k').context); // (R1:2)

  // 离线客户端只见过 (R1:1)，提交过期上下文
  const stale = VersionVector.from({ R1: 1 });
  const res = r1.put('k', 'offline-edit', stale);
  assert.equal(res.outcome, 'sibling-fork');

  const read = r1.get('k');
  assert.equal(read.siblings.length, 2);
  assert.deepEqual(
    read.siblings.map((s) => s.value).sort(),
    ['offline-edit', 'v2'],
  );
});

test('resolve 收敛所有分支', () => {
  const r1 = new Replica('R1');
  r1.put('k', 'a', VersionVector.empty());
  r1.put('k', 'b', VersionVector.empty()); // 并发离线写入 → 分叉
  assert.equal(r1.get('k').siblings.length, 2);

  const res = r1.resolve('k', 'merged');
  assert.equal(res.outcome, 'fast-overwrite');
  assert.equal(r1.get('k').siblings.length, 1);
  assert.equal(r1.get('k').siblings[0].value, 'merged');
});
