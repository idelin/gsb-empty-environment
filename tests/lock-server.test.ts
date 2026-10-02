import { test } from "node:test";
import assert from "node:assert/strict";
import { setTimeout as sleep } from "node:timers/promises";
import { LockServer } from "../src/lock-server.ts";
import type { LeaseSnapshot } from "../src/lock-server.ts";
import {
  AcquireAbortedError,
  LeaseLostError,
  NotLockHolderError,
} from "../src/errors.ts";

test("锁空闲时立即授权，fence token 单调递增", async () => {
  const server = new LockServer();
  const a = await server.acquire("res-1", "A", 1000);
  const b = await server.acquire("res-2", "B", 1000);
  assert.equal(a.ownerId, "A");
  assert.equal(a.fenceToken, 1);
  assert.equal(b.fenceToken, 2);
  assert.ok(b.fenceToken > a.fenceToken);
  server.close();
});

test("锁被占用时排队，释放后按 FIFO 顺序授予等待者", async () => {
  const server = new LockServer();
  const a = await server.acquire("res", "A", 1000);

  let bGranted: LeaseSnapshot | null = null;
  const bPromise = server.acquire("res", "B", 1000).then((s) => {
    bGranted = s;
    return s;
  });
  const cPromise = server.acquire("res", "C", 1000);

  await sleep(50);
  assert.equal(bGranted, null, "A 持有期间 B 不应获锁");
  assert.equal(server.waiterCount("res"), 2);

  server.release(a.leaseId);
  const b = await bPromise;
  assert.equal(b.ownerId, "B");
  assert.ok(b.fenceToken > a.fenceToken, "重新授权应颁发更大的 fence token");

  server.release(b.leaseId);
  const c = await cPromise;
  assert.equal(c.ownerId, "C");
  server.close();
});

test("renew 延长租约；对未知租约续期抛出 LeaseLostError", async () => {
  const server = new LockServer();
  const lease = await server.acquire("res", "A", 500);
  const before = lease.expiresAt;
  await sleep(50);
  const renewed = server.renew(lease.leaseId, 500);
  assert.ok(renewed.expiresAt > before, "续期后到期时间应后移");
  assert.throws(() => server.renew("no-such-lease", 500), LeaseLostError);
  server.close();
});

test("不续期则租约 TTL 到期自动释放，等待者被唤醒", async () => {
  const server = new LockServer();
  const lease = await server.acquire("res", "A", 200);
  const bPromise = server.acquire("res", "B", 200);
  await sleep(50);
  assert.equal(server.waiterCount("res"), 1);

  const b = await bPromise;
  const elapsed = Date.now() - (lease.expiresAt - 200);
  assert.equal(b.ownerId, "B");
  assert.ok(elapsed >= 190, `租约应约 200ms 后过期，实际 ${elapsed}ms`);
  assert.equal(server.inspect("res")?.ownerId, "B");
  assert.throws(() => server.renew(lease.leaseId, 200), LeaseLostError);
  server.close();
});

test("释放未知租约抛出 NotLockHolderError", async () => {
  const server = new LockServer();
  await server.acquire("res", "A", 1000);
  assert.throws(() => server.release("not-a-lease"), NotLockHolderError);
  server.close();
});

test("AbortSignal 可取消排队中的 acquire", async () => {
  const server = new LockServer();
  const a = await server.acquire("res", "A", 1000);
  const controller = new AbortController();
  const pending = server.acquire("res", "B", 1000, controller.signal);
  await sleep(20);
  controller.abort();
  await assert.rejects(pending, AcquireAbortedError);
  assert.equal(server.waiterCount("res"), 0);
  server.release(a.leaseId);
  server.close();
});
