import { test } from "node:test";
import assert from "node:assert/strict";
import { setTimeout as sleep } from "node:timers/promises";
import { LeaseServer } from "../src/leaseServer.ts";
import { CacheClient } from "../src/client.ts";

const FILE = "X";

test("验收边界:租约 5s,第 2s 写入,客户端 1 失联 -> 服务端安全阻塞满 5s 后放行,零读脏", async () => {
  const server = new LeaseServer();
  const client1 = new CacheClient("client-1", server);
  const client2 = new CacheClient("client-2", server);

  // 初始内容
  await client2.write(FILE, "v0");
  const versionBefore = server.readVersion(FILE);

  // t=0:客户端 1 获得文件 X 为期 5 秒的读租约
  const t0 = Date.now();
  const data1 = await client1.read(FILE);
  assert.equal(data1, "v0");
  assert.equal(server.activeLeaseCount(FILE), 1);

  // 客户端 1 失联:不响应撤销回调
  client1.simulateUnreachable = true;

  // t=2s:客户端 2 尝试修改 X
  await sleep(2_000 - (Date.now() - t0));
  let writeDone = false;
  const writePromise = client2.write(FILE, "v1").then((r) => {
    writeDone = true;
    return r;
  });

  // 写阻塞期间:租约期内客户端 1 读本地缓存仍是安全的旧值(协议允许)
  await sleep(500);
  assert.equal(writeDone, false, "租约未过期,写入必须仍被阻塞");
  assert.equal(await client1.read(FILE), "v0");
  assert.equal(server.readVersion(FILE), versionBefore, "租约失效前服务端不得落盘");

  // 等待写入完成
  const result = await writePromise;
  const elapsed = Date.now() - t0;

  // 服务端阻塞至租约满 5 秒后才放行
  assert.ok(elapsed >= 4_900, `写入放行时刻 ${elapsed}ms 必须 >= ~5000ms`);
  assert.ok(result.waitedMs >= 2_900, `阻塞时长 ${result.waitedMs}ms 应约等于剩余租约 3s`);
  assert.equal(result.revokedLeases, 1);
  assert.equal(server.revokeNotifications, 1, "服务端应已通知客户端 1 撤销");
  assert.equal(server.activeLeaseCount(FILE), 0);

  // 零读脏:写入落盘后,任何新读取都必须看到新值
  const client3 = new CacheClient("client-3", server);
  assert.equal(await client3.read(FILE), "v1");
  // 失联的客户端 1 租约已过期,本地缓存失效,重新读取也必须拿到新值
  assert.equal(await client1.read(FILE), "v1");
});

test("快速路径:持租客户端在线,收到撤销后立即 ack,写入无需等满租约", async () => {
  const server = new LeaseServer();
  const client1 = new CacheClient("client-1", server);
  const client2 = new CacheClient("client-2", server);

  await client2.write(FILE, "v0");
  assert.equal(await client1.read(FILE), "v0"); // 5s 租约
  assert.ok(client1.hasValidCache(FILE));

  const result = await client2.write(FILE, "v1");
  assert.ok(result.waitedMs < 1_000, `在线 ack 应快速放行,实际 ${result.waitedMs}ms`);
  assert.equal(result.revokedLeases, 1);
  assert.equal(client1.hasValidCache(FILE), false, "撤销后本地缓存必须已失效");
  assert.equal(await client1.read(FILE), "v1");
});

test("租约期内重复读命中本地缓存,不产生服务端往返", async () => {
  const server = new LeaseServer();
  const client1 = new CacheClient("client-1", server);
  const client2 = new CacheClient("client-2", server);

  await client2.write(FILE, "v0");
  await client1.read(FILE);
  await client1.read(FILE);
  await client1.read(FILE);

  assert.equal(client1.localHits, 2);
  assert.equal(server.leasesGranted, 1, "只应申请一次租约");
});

test("无活跃租约时写入立即完成", async () => {
  const server = new LeaseServer();
  const client = new CacheClient("client-1", server);

  const result = await client.write(FILE, "v0");
  assert.ok(result.waitedMs < 100);
  assert.equal(result.revokedLeases, 0);
  assert.equal(result.version, 1);
});

test("写阻塞期间新租约申请被挂起,落盘后按新数据发放(防脏读窗口)", async () => {
  const server = new LeaseServer();
  const client1 = new CacheClient("client-1", server);
  const client2 = new CacheClient("client-2", server);
  const client3 = new CacheClient("client-3", server);

  await client2.write(FILE, "v0");
  await client1.readWithLease(FILE, 300);
  client1.simulateUnreachable = true;

  const writePromise = client2.write(FILE, "v1");
  await sleep(50); // 确保写已进入阻塞
  const readPromise = client3.read(FILE); // 应被挂起直到写落盘

  const [writeResult, data3] = await Promise.all([writePromise, readPromise]);
  assert.ok(writeResult.waitedMs >= 200);
  assert.equal(data3, "v1", "写阻塞期间申请的租约必须拿到写后的新数据");
});

test("同一文件并发写入按序串行执行,版本单调递增", async () => {
  const server = new LeaseServer();
  const client = new CacheClient("client-1", server);

  const [r1, r2, r3] = await Promise.all([
    client.write(FILE, "a"),
    client.write(FILE, "b"),
    client.write(FILE, "c"),
  ]);
  assert.deepEqual([r1.version, r2.version, r3.version], [1, 2, 3]);
  const reader = new CacheClient("reader", server);
  assert.equal(await reader.read(FILE), "c");
});
