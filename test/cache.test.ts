import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ByteCache, measureSize } from '../src/cache.ts';

const META = { generation: 0 } as const;

function makeCache(quotas: Record<string, number> = { a: 400, b: 400, c: 200 }) {
  const total = Object.values(quotas).reduce((x, y) => x + y, 0);
  return new ByteCache(total, quotas);
}

test('额度按实际条目大小计：不同大小条目消耗不同额度', () => {
  const cache = makeCache();
  const small = 'x'.repeat(98); // key(2) + 98 = 100
  const large = 'y'.repeat(190); // key(2) + 190 = 192
  assert.equal(measureSize('k1') + measureSize(small), 100);
  cache.set('a', 'k1', small, META);
  assert.equal(cache.stats().partitions.a.used, 100);
  cache.set('a', 'k2', large, META);
  assert.equal(cache.stats().partitions.a.used, 292);
});

test('LRU 逐出：最久未用条目先被逐出', () => {
  const cache = makeCache();
  cache.set('a', 'k1', 'x'.repeat(98), META);
  cache.set('a', 'k2', 'x'.repeat(98), META);
  cache.set('a', 'k3', 'x'.repeat(98), META);
  cache.get('a', 'k1'); // 刷新 k1
  cache.set('a', 'k4', 'x'.repeat(148), META); // 300+150>400，逐出最旧的 k2
  assert.ok(cache.peek('a', 'k1'));
  assert.equal(cache.peek('a', 'k2'), undefined);
  assert.ok(cache.peek('a', 'k3'));
  assert.ok(cache.peek('a', 'k4'));
  assert.equal(cache.stats().partitions.a.used, 350);
});

test('驱逐借用：本分区不足时先逐出再借用，总额度守恒', () => {
  const cache = makeCache({ a: 100, b: 300, c: 600 });
  cache.set('a', 'k1', 'x'.repeat(98), META); // a 用满 100
  const ok = cache.set('a', 'k2', 'y'.repeat(148), META); // 逐出 k1，再借 50
  assert.equal(ok, true);
  assert.equal(cache.peek('a', 'k1'), undefined);
  assert.ok(cache.peek('a', 'k2'));
  const stats = cache.stats();
  assert.equal(stats.partitions.a.quota, 150);
  assert.equal(stats.partitions.a.used, 150);
  // 借用方额度减少等量字节，总额守恒
  assert.equal(stats.totalQuota, 1000);
  assert.equal(stats.conserved, true);
  const lenderDelta = 300 - stats.partitions.b.quota + (600 - stats.partitions.c.quota);
  assert.equal(lenderDelta, 50);
});

test('字节额度守恒：混合写入逐出后总量不变且用量不超额', () => {
  const cache = makeCache({ a: 200, b: 200, c: 100 });
  for (let i = 0; i < 50; i++) {
    const partition = ['a', 'b', 'c'][i % 3];
    cache.set(partition, `key-${i}`, 'v'.repeat(30 + (i % 40)), META);
    const stats = cache.stats();
    assert.equal(stats.totalQuota, 500, `第 ${i} 次写入后配额守恒`);
    assert.ok(stats.totalUsed <= 500, `第 ${i} 次写入后用量不超额`);
  }
});

test('精确失效：仅清除目标字体的旧版本条目', () => {
  const cache = makeCache();
  cache.set('a', 'm:serif@1', 'v1', { fontId: 'serif', fontVersion: 1, generation: 0 });
  cache.set('a', 'm:serif@2', 'v2', { fontId: 'serif', fontVersion: 2, generation: 1 });
  cache.set('b', 'm:mono@1', 'mv1', { fontId: 'mono', fontVersion: 1, generation: 0 });
  const removed = cache.invalidateWhere(
    (meta) => meta.fontId === 'serif' && meta.fontVersion !== 2,
  );
  assert.equal(removed, 1);
  assert.equal(cache.peek('a', 'm:serif@1'), undefined);
  assert.ok(cache.peek('a', 'm:serif@2'));
  assert.ok(cache.peek('b', 'm:mono@1'));
});

test('写入代际下限：旧代际回包被拒绝', () => {
  const cache = makeCache();
  cache.raiseWriteFloor(3);
  assert.equal(cache.set('a', 'old', 'x', { generation: 2 }), false);
  assert.equal(cache.set('a', 'new', 'x', { generation: 3 }), true);
});
