import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HashRing } from '../src/ring.ts';
import { murmur3_32 } from '../src/hashes.ts';
import { randomKeyGen } from '../src/analyzer.ts';

const NODES = ['a', 'b', 'c', 'd'];

test('空节点列表抛错', () => {
  assert.throws(() => new HashRing([], murmur3_32), /不能为空/);
});

test('单节点环：所有键都映射到该节点', () => {
  const ring = new HashRing(['only'], murmur3_32, 64);
  const gen = randomKeyGen(1);
  for (let i = 0; i < 1000; i++) {
    assert.equal(ring.getNode(gen(i)), 'only');
  }
});

test('环统计：虚拟节点总数与碰撞数自洽', () => {
  const ring = new HashRing(NODES, murmur3_32, 160);
  assert.equal(ring.stats.totalPoints, 4 * 160);
  assert.equal(
    ring.stats.distinctPositions + ring.stats.collisions,
    ring.stats.totalPoints,
  );
});

test('一致性：摘除一个节点仅约 1/N 的键迁移', () => {
  const before = new HashRing(NODES, murmur3_32, 160);
  const after = new HashRing(NODES.slice(0, 3), murmur3_32, 160);
  const gen = randomKeyGen(7);
  const total = 20000;
  let moved = 0;
  let movedToRemoved = 0;
  for (let i = 0; i < total; i++) {
    const key = gen(i);
    const b = before.getNode(key);
    const a = after.getNode(key);
    if (a !== b) {
      moved++;
      if (b === 'd') movedToRemoved++;
    }
  }
  // 迁移的键必须全部来自被摘除的节点
  assert.equal(moved, movedToRemoved);
  // 迁移比例应接近 1/4（放宽到 15%~35%）
  const ratio = moved / total;
  assert.ok(ratio > 0.15 && ratio < 0.35, `迁移比例 ${ratio} 异常`);
});
