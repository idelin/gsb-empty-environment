import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LeaseServer } from '../src/lease-server.ts';
import { CacheClient } from '../src/cache-client.ts';

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

test('租约期内读命中本地缓存,不重复访问服务端存储', async () => {
  const server = new LeaseServer({ leaseDurationMs: 300 });
  server.seed('X', 'v1');
  const c1 = new CacheClient('client-1', server);

  assert.equal(await c1.read('X'), 'v1');
  assert.equal(await c1.read('X'), 'v1');
  assert.equal(await c1.read('X'), 'v1');
  assert.equal(server.storageReads, 1, '租约期内只回源一次');
});

test('租约过期后重新向服务端获取租约与内容', async () => {
  const server = new LeaseServer({ leaseDurationMs: 120 });
  server.seed('X', 'v1');
  const c1 = new CacheClient('client-1', server);

  assert.equal(await c1.read('X'), 'v1');
  await sleep(200); // 等租约过期
  assert.equal(await c1.read('X'), 'v1');
  assert.equal(server.storageReads, 2, '过期后重新回源');
});

test('验收场景:持有者失联时,写入安全阻塞至租约期满(零读脏)', async () => {
  const LEASE_MS = 500;
  const server = new LeaseServer({ leaseDurationMs: LEASE_MS });
  server.seed('X', 'v1');
  const c1 = new CacheClient('client-1', server);
  const c2 = new CacheClient('client-2', server);

  const t0 = server.currentTime();
  assert.equal(await c1.read('X'), 'v1'); // t=0: 客户端1获得读租约

  c1.disconnect(); // 客户端1失联:撤销回调无法送达

  // t≈200ms: 客户端2发起写入
  await sleep(200);
  let writeDoneAt = -1;
  const writePromise = c2.write('X', 'v2').then(() => {
    writeDoneAt = server.currentTime() - t0;
  });

  // 阻塞期间:客户端1读本地缓存仍是 v1,且服务端保证其有效性(零读脏)
  await sleep(150);
  assert.equal(await c1.read('X'), 'v1');
  assert.equal(server.read('X'), 'v1', '租约期满前写入不得落盘');
  assert.equal(writeDoneAt, -1, '租约期满前写入不得完成');

  await writePromise;
  assert.ok(
    writeDoneAt >= LEASE_MS,
    `写入必须等到租约期满(实际 ${writeDoneAt}ms >= ${LEASE_MS}ms)`,
  );
  assert.equal(server.read('X'), 'v2');

  // 写完成后,任何新读取都得到 v2
  const c3 = new CacheClient('client-3', server);
  assert.equal(await c3.read('X'), 'v2');
});

test('持有者在线时,撤销回调使写入提前放行', async () => {
  const LEASE_MS = 5000; // 长租约:若等满则测试明显变慢
  const server = new LeaseServer({ leaseDurationMs: LEASE_MS });
  server.seed('X', 'v1');
  const c1 = new CacheClient('client-1', server);
  const c2 = new CacheClient('client-2', server);

  const t0 = server.currentTime();
  assert.equal(await c1.read('X'), 'v1');

  await sleep(100);
  await c2.write('X', 'v2');
  const elapsed = server.currentTime() - t0;

  assert.equal(c1.revokeNotifications, 1, '客户端1收到撤销通知');
  assert.ok(
    elapsed < LEASE_MS,
    `撤销确认后应提前放行(实际 ${elapsed}ms << ${LEASE_MS}ms)`,
  );
  assert.equal(c1.hasValidCache('X'), false, '客户端1缓存已被失效');
  assert.equal(await c1.read('X'), 'v2', '客户端1重新读取获得新值');
});

test('多个读者时,写者等待最晚过期的租约', async () => {
  const LEASE_MS = 400;
  const server = new LeaseServer({ leaseDurationMs: LEASE_MS });
  server.seed('X', 'v1');
  const c1 = new CacheClient('client-1', server);
  const c2 = new CacheClient('client-2', server);
  const writer = new CacheClient('writer', server);

  const t0 = server.currentTime();
  await c1.read('X'); // t=0 获得租约
  await sleep(200);
  await c2.read('X'); // t=200 获得租约,600 才过期
  c1.disconnect();
  c2.disconnect();

  let writeDoneAt = -1;
  const writePromise = writer.write('X', 'v2').then(() => {
    writeDoneAt = server.currentTime() - t0;
  });

  await writePromise;
  assert.ok(
    writeDoneAt >= 600,
    `必须等待最晚租约过期(实际 ${writeDoneAt}ms >= 600ms)`,
  );
  assert.equal(server.read('X'), 'v2');
});

test('同一文件的并发写被串行化,且均遵守租约约束', async () => {
  const server = new LeaseServer({ leaseDurationMs: 300 });
  server.seed('X', 'v0');
  const reader = new CacheClient('reader', server);
  const w1 = new CacheClient('w1', server);
  const w2 = new CacheClient('w2', server);

  const t0 = server.currentTime();
  await reader.read('X'); // 租约至 t=300
  reader.disconnect();

  await Promise.all([w1.write('X', 'v1'), w2.write('X', 'v2')]);
  const elapsed = server.currentTime() - t0;

  assert.ok(elapsed >= 300, `两次写都不得早于租约过期(实际 ${elapsed}ms)`);
  assert.equal(server.read('X'), 'v2', '后写覆盖先写');
});
