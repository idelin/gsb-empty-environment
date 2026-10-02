import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MemoryGuard } from '../src/memory-guard.ts';
import { sleep } from './helpers.ts';

test('限流器：在途字节达到上限时阻塞，释放后放行', async () => {
  const guard = new MemoryGuard(1_000);
  await guard.acquire(600);
  await guard.acquire(400);
  assert.equal(guard.usage, 1_000);

  let thirdPassed = false;
  const third = guard.acquire(200).then(() => {
    thirdPassed = true;
  });
  await sleep(20);
  assert.equal(thirdPassed, false, '超额 acquire 必须挂起等待');

  guard.release(400);
  await third;
  assert.equal(thirdPassed, true, '释放额度后等待者被放行');
  assert.equal(guard.usage, 800);
});

test('限流器：等待者按 FIFO 顺序放行', async () => {
  const guard = new MemoryGuard(100);
  await guard.acquire(100);
  const order: string[] = [];
  const a = guard.acquire(50).then(() => order.push('a'));
  const b = guard.acquire(50).then(() => order.push('b'));
  await sleep(10);
  guard.release(100);
  await Promise.all([a, b]);
  assert.deepEqual(order, ['a', 'b']);
});

test('限流器：峰值统计不超过上限', async () => {
  const guard = new MemoryGuard(512);
  const workers = Array.from({ length: 16 }, async () => {
    await guard.acquire(128);
    await sleep(5);
    guard.release(128);
  });
  await Promise.all(workers);
  assert.ok(guard.peakUsage <= 512, '峰值不应超过上限');
  assert.equal(guard.usage, 0);
});

test('限流器：单个超限分片在空闲时独占通过，避免死锁', async () => {
  const guard = new MemoryGuard(100);
  await guard.acquire(500);
  assert.equal(guard.usage, 500);
  guard.release(500);
  assert.equal(guard.usage, 0);
});

test('限流器：非法上限抛错', () => {
  assert.throws(() => new MemoryGuard(0), RangeError);
});
