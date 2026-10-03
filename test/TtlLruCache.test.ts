import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as sleep } from 'node:timers/promises';
import { TtlLruCache } from '../src/TtlLruCache.ts';

/** 可手动推进的假时钟，保证时间语义测试确定性。 */
function fakeClock(start = 1_000_000) {
  let t = start;
  return {
    now: () => t,
    advance: (ms: number) => { t += ms; },
  };
}

test('基础读写与覆盖更新', () => {
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 0, now: clock.now });
  cache.set('a', '1');
  assert.equal(cache.get('a'), '1');
  cache.set('a', '2');
  assert.equal(cache.get('a'), '2');
  assert.equal(cache.size, 1);
  assert.equal(cache.pendingExpirations, 0); // 无 TTL 不入时间堆
  cache.dispose();
});

test('LRU 淘汰：get 单次操作内 O(1) 提升热度，改变淘汰顺序', () => {
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ maxSize: 3, sweepIntervalMs: 0, now: clock.now });
  cache.set('a', '1');
  cache.set('b', '2');
  cache.set('c', '3');
  cache.get('a'); // 提升 a，最冷变为 b
  cache.set('d', '4');
  assert.equal(cache.has('b'), false, '最冷的 b 应被淘汰');
  assert.equal(cache.get('a'), '1');
  assert.equal(cache.get('c'), '3');
  assert.equal(cache.get('d'), '4');
  cache.dispose();
});

test('绝对 TTL：读取时判定已超时立即返回空并摘除', () => {
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 0, now: clock.now });
  cache.set('x', 'v', { ttlMs: 50 });
  clock.advance(49);
  assert.equal(cache.get('x'), 'v');
  clock.advance(2); // t = +51ms
  assert.equal(cache.get('x'), undefined, '超时读取必须返回空');
  assert.equal(cache.size, 0, '超时对象被立即摘除');
  assert.equal(cache.pendingExpirations, 0);
  cache.dispose();
});

test('滑动过期（TTD）：访问续期，停止访问后到期', () => {
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 0, now: clock.now });
  cache.set('s', 'v', { slidingMs: 100 });
  for (let i = 0; i < 5; i++) {
    clock.advance(90); // 每 90ms 访问一次，窗口 100ms
    assert.equal(cache.get('s'), 'v', `第 ${i + 1} 次续期后仍存活`);
  }
  clock.advance(101); // 超过滑动窗口
  assert.equal(cache.get('s'), undefined);
  cache.dispose();
});

test('滑动续期不得突破绝对 TTL 硬上限', () => {
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 0, now: clock.now });
  cache.set('cap', 'v', { ttlMs: 120, slidingMs: 100 });
  clock.advance(90);
  assert.equal(cache.get('cap'), 'v'); // 滑动续期到 min(120, 190) = 120
  clock.advance(31); // t = +121ms，突破绝对上限
  assert.equal(cache.get('cap'), undefined);
  cache.dispose();
});

test('批量剔除：sweepExpired 单次调用清除全部已过期对象', () => {
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 0, now: clock.now });
  for (let i = 0; i < 50; i++) cache.set(`dead${i}`, 'v', { ttlMs: 10 + i });
  for (let i = 0; i < 10; i++) cache.set(`live${i}`, 'v', { ttlMs: 10_000 });
  clock.advance(60); // 50 个死对象全部到期
  const removed = cache.sweepExpired();
  assert.equal(removed, 50, '一次批量剔除 50 个死对象');
  assert.equal(cache.size, 10);
  assert.equal(cache.pendingExpirations, 10);
  cache.dispose();
});

test('验收场景：100 个对象、10~100ms 不等 TTL、高频访问、后台批量回收、零泄漏', async () => {
  // 注入可控时钟保证时间语义确定；后台清扫器仍用真实 5ms 定时器批量回收。
  const clock = fakeClock();
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 5, now: clock.now });
  const HOT = 20;

  // 插入 100 个对象，TTL 从 10ms 到 100ms 不等。
  // 前 20 个热点对象：绝对上限 100ms + 30ms 滑动窗口——
  // 若无人访问，30ms 即死；高频访问可续命至绝对上限。
  for (let i = 0; i < 100; i++) {
    const hot = i < HOT;
    const ttlMs = hot ? 100 : 10 + ((i - HOT) * 90) / (99 - HOT);
    cache.set(`k${i}`, `v${i}`, hot ? { ttlMs, slidingMs: 30 } : { ttlMs });
  }
  // 对照组：与热点参数相同但从不访问，30ms 滑动窗口耗尽即死
  cache.set('control', 'v', { ttlMs: 100, slidingMs: 30 });
  assert.equal(cache.size, 101);
  assert.equal(cache.pendingExpirations, 101);

  // 高频访问热点对象：推进 45ms，每 4ms 访问一轮（滑动窗口 30ms）
  for (let t = 0; t < 45; t += 4) {
    clock.advance(4);
    for (let i = 0; i < HOT; i++) cache.get(`k${i}`);
  }
  // 滑动续期生效：若无续期它们早已在 ~30ms 时死亡
  for (let i = 0; i < HOT; i++) {
    assert.equal(cache.get(`k${i}`), `v${i}`, `热点 k${i} 应被滑动续期保住`);
  }
  assert.equal(cache.get('control'), undefined, '对照组未续期，30ms 即死');

  // 推进时钟越过所有绝对 TTL（100ms），后台清扫器按真实 5ms 周期批量回收
  clock.advance(200);
  for (let waited = 0; cache.size > 0 && waited < 2000; waited += 10) {
    await sleep(10); // 等待后台批量回收完成
  }
  for (let i = 0; i < 100; i++) {
    assert.equal(cache.get(`k${i}`), undefined, `k${i} 超时读取必须返回空`);
  }
  assert.equal(cache.size, 0, '全部对象被回收，无残留');
  assert.equal(cache.pendingExpirations, 0, '时间堆已排空，内存零泄漏');

  cache.dispose();
  assert.equal(cache.sweeping, false, '后台清扫器已停止，无定时器泄漏');
});

test('dispose 后后台清扫器停止，不再自动回收', async () => {
  const cache = new TtlLruCache<string>({ sweepIntervalMs: 5 });
  cache.set('a', '1', { ttlMs: 10 });
  cache.dispose();
  await sleep(30);
  assert.equal(cache.size, 0, 'dispose 已清空缓存');
  cache.set('b', '2', { ttlMs: 10 });
  await sleep(30);
  assert.equal(cache.size, 1, '定时器已清除，不再自动清扫');
  assert.equal(cache.get('b'), undefined, '但惰性过期判定仍生效');
});
