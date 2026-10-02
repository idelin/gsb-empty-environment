import { test } from "node:test";
import assert from "node:assert/strict";
import { setTimeout as sleep } from "node:timers/promises";
import { LockServer } from "../src/lock-server.ts";
import { LockClient, LockHandle } from "../src/lock-client.ts";
import { FencedStore } from "../src/fenced-store.ts";
import { LeaseLostError, StaleFenceTokenError } from "../src/errors.ts";

test("心跳续期使锁存活时间远超原始 TTL", async () => {
  const server = new LockServer();
  const clientA = new LockClient(server, "A");
  const clientB = new LockClient(server, "B");

  const handleA = await clientA.acquire("res", {
    ttlMs: 300,
    heartbeatIntervalMs: 100,
  });
  let bGranted = false;
  const bPromise = clientB.acquire("res", { ttlMs: 300 }).then((h) => {
    bGranted = true;
    return h;
  });

  await sleep(700); // 远超 300ms 的 TTL
  assert.equal(bGranted, false, "心跳正常时 B 不应获锁");
  assert.ok(handleA.isHeld);
  assert.ok(handleA.expiresAt > Date.now(), "租约应被心跳不断延长");

  handleA.release();
  const handleB = await bPromise;
  assert.ok(handleB.isHeld);
  handleB.release();
  server.close();
});

test("假死停止心跳 → 租约过期 → 等待者获锁 → 恢复后句柄标记为 lost", async () => {
  const server = new LockServer();
  const clientA = new LockClient(server, "A");
  const clientB = new LockClient(server, "B");

  const handleA = await clientA.acquire("res", {
    ttlMs: 300,
    heartbeatIntervalMs: 100,
  });
  const bPromise = clientB.acquire("res", { ttlMs: 300 });
  await sleep(50);

  handleA.suspend(); // 模拟崩溃：心跳停止
  const handleB = await bPromise; // 租约约 300ms 后过期，B 获锁
  assert.ok(handleB.isHeld);
  assert.ok(handleB.fenceToken > handleA.fenceToken);

  let lostErr: unknown = null;
  handleA.on("lost", (err) => {
    lostErr = err;
  });
  handleA.resume(); // 恢复时发现锁已易主
  assert.equal(handleA.isHeld, false);
  assert.equal(handleA.handleState, "lost");
  assert.ok(lostErr instanceof LeaseLostError);
  assert.throws(() => handleA.ensureHeld(), LeaseLostError);

  handleB.release();
  server.close();
});

test("快速恢复（租约未过期）则继续保持锁", async () => {
  const server = new LockServer();
  const clientA = new LockClient(server, "A");
  const handleA = await clientA.acquire("res", {
    ttlMs: 500,
    heartbeatIntervalMs: 150,
  });
  handleA.suspend();
  await sleep(100); // 小于 TTL，租约尚未过期
  handleA.resume();
  await sleep(50);
  assert.ok(handleA.isHeld, "租约未过期时恢复应继续持锁");
  handleA.release();
  server.close();
});

test("FencedStore 拒绝过期或重复的 fence token", () => {
  const store = new FencedStore();
  store.write(1, "k", "v1");
  store.write(2, "k", "v2");
  assert.throws(() => store.write(1, "k", "v3"), StaleFenceTokenError);
  assert.throws(() => store.write(2, "k", "v3"), StaleFenceTokenError);
  assert.equal(store.read("k"), "v2");
  assert.equal(store.currentFenceToken, 2);
});

test("主动 release 后等待者获锁，且心跳已停止", async () => {
  const server = new LockServer();
  const clientA = new LockClient(server, "A");
  const clientB = new LockClient(server, "B");
  const handleA: LockHandle = await clientA.acquire("res", { ttlMs: 400 });
  const bPromise = clientB.acquire("res", { ttlMs: 400 });
  handleA.release();
  const handleB = await bPromise;
  assert.ok(handleB.isHeld);
  assert.equal(handleA.handleState, "released");
  handleA.release(); // 幂等
  handleB.release();
  server.close();
});
