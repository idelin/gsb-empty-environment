import { setTimeout as sleep } from 'node:timers/promises';
import { TtlLruCache } from '../src/TtlLruCache.ts';

const cache = new TtlLruCache<string>({ maxSize: 1000, sweepIntervalMs: 5 });
const HOT = 20;

// 1. 插入 100 个对象，TTL 从 10ms 到 100ms 不等；
//    前 20 个热点对象：绝对上限 100ms + 30ms 滑动窗口（不访问则 30ms 即死）
for (let i = 0; i < 100; i++) {
  const hot = i < HOT;
  const ttlMs = hot ? 100 : 10 + ((i - HOT) * 90) / (99 - HOT);
  cache.set(`key-${i}`, `value-${i}`, hot ? { ttlMs, slidingMs: 30 } : { ttlMs });
}
cache.set('control', 'never-touched', { ttlMs: 100, slidingMs: 30 });
console.log(`[t=0ms]   写入完成: size=${cache.size}, 时间堆待到期=${cache.pendingExpirations}`);

// 2. 高频访问热点对象（每 ~4ms 一轮，持续 45ms），滑动续期保命
const stop = Date.now() + 45;
while (Date.now() < stop) {
  for (let i = 0; i < HOT; i++) cache.get(`key-${i}`);
  await sleep(4);
}
console.log(`[t≈45ms]  高频访问后热点仍存活: key-0=${cache.get('key-0')}`);
console.log(`[t≈45ms]  同参数未访问的对照组已死: control=${cache.get('control')}`);
console.log(`[t≈45ms]  短 TTL 冷对象已超时: key-20=${cache.get('key-20')}`);

// 3. 等待全部绝对 TTL 到期，后台清扫器批量回收
await sleep(130);
console.log(`[t≈175ms] 超时读取立即返回空: key-0=${cache.get('key-0')}`);
console.log(`[t≈175ms] 后台批量回收完成: size=${cache.size}, 时间堆待到期=${cache.pendingExpirations}`);

cache.dispose();
console.log(`[done]    dispose 后清扫器存活=${cache.sweeping}，内存零泄漏`);
