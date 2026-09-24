import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReactiveCache, SubscriptionRejectedError, verifyLedger } from '../src/index.ts';

function makeCache(opts: Partial<{ budget: number; cacheBudget: number }> = {}): ReactiveCache {
  return new ReactiveCache({ budget: opts.budget ?? 1_000_000, cacheBudget: opts.cacheBudget ?? 100_000 });
}

test('派生值只算一次，之后命中缓存', async () => {
  const c = makeCache();
  c.defineSource('a', 2);
  c.defineSource('b', 3);
  c.defineDerived('sum', ['a', 'b'], ([a, b]) => (a as number) + (b as number));

  assert.equal(await c.read('sum'), 5);
  assert.equal(await c.read('sum'), 5);
  assert.equal(c.stats.computes, 1);
  assert.equal(c.stats.hits, 1);
  assert.ok(verifyLedger(c).ok);
});

test('依赖变更后绝不命中旧缓存（含传递依赖）', async () => {
  const c = makeCache();
  c.defineSource('a', 1);
  c.defineDerived('double', ['a'], ([a]) => (a as number) * 2);
  c.defineDerived('quad', ['double'], ([d]) => (d as number) * 2);

  assert.equal(await c.read('quad'), 4);
  await c.transact((tx) => tx.set('a', 5));
  // 直接命中旧缓存会返回 4；正确行为是重算出 20
  assert.equal(await c.read('quad'), 20);
  assert.equal(await c.read('double'), 10);
  assert.ok(verifyLedger(c).ok);
});

test('驱逐按固定权重策略选择受害者', async () => {
  const c = makeCache();
  c.defineSource('s', 1);
  c.defineDerived('d1', ['s'], ([s]) => (s as number) + 1);
  c.defineDerived('d2', ['s'], ([s]) => (s as number) + 2);
  c.defineDerived('d3', ['s'], ([s]) => (s as number) + 3);

  await c.read('d1');
  await c.read('d2');
  await c.read('d3');
  await c.read('d1'); // d1 权重更高（hits=1）

  // d2/d3 权重相同(0)，d2 lastUsed 更早 → 先被驱逐
  assert.deepEqual(c.flush(), ['d2', 'd3', 'd1']);
  assert.ok(verifyLedger(c).ok);
});

test('驱逐只释放可重算值：订阅保留，源状态不动，重算结果相同', async () => {
  const c = makeCache();
  c.defineSource('a', 7);
  c.defineDerived('keep', ['a'], ([a]) => (a as number) * 3);
  c.defineDerived('drop', ['a'], ([a]) => (a as number) + 100);

  const before = await c.read('drop');
  const sub = await c.subscribe('keep');
  const evicted = c.flush();

  assert.deepEqual(evicted, ['drop']); // 订阅的 keep 被保留
  assert.equal(c.store.readCell('a'), 7); // 源状态不受驱逐影响

  const after = await c.read('drop'); // 驱逐后首次读触发重算
  assert.deepEqual(after, before); // 结果相同
  assert.equal(c.stats.computes, 3); // keep 1 次 + drop 2 次

  sub.unsubscribe();
  assert.deepEqual(c.flush(), ['keep', 'drop']); // 退订后两者均可驱逐，keep 更旧先走
  assert.ok(verifyLedger(c).ok);
});

test('缓存 0 容量仍功能正确', async () => {
  const c = makeCache({ cacheBudget: 0 });
  c.defineSource('a', 2);
  c.defineSource('b', 5);
  c.defineDerived('sum', ['a', 'b'], ([a, b]) => (a as number) + (b as number));
  c.defineDerived('product', ['a', 'b'], ([a, b]) => (a as number) * (b as number));
  c.defineDerived('combo', ['sum', 'product'], ([s, p]) => ({ s, p }));

  assert.deepEqual(await c.read('combo'), { s: 7, p: 10 });
  assert.deepEqual(await c.read('combo'), { s: 7, p: 10 }); // 每次都重算但结果正确
  assert.equal(c.cacheBytes, 0);

  await c.transact((tx) => tx.set('a', 4));
  assert.deepEqual(await c.read('combo'), { s: 9, p: 20 });

  // 0 容量下无法保留订阅值 → 明确拒绝
  await assert.rejects(c.subscribe('sum'), SubscriptionRejectedError);
  assert.ok(verifyLedger(c).ok);
  assert.deepEqual(c.scheduler.checkQuiescent(), []);
});
