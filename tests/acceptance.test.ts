import { test } from "node:test";
import assert from "node:assert/strict";
import { setTimeout as sleep } from "node:timers/promises";
import { LockServer } from "../src/lock-server.ts";
import { LockClient } from "../src/lock-client.ts";
import { FencedStore } from "../src/fenced-store.ts";
import { LeaseLostError, StaleFenceTokenError } from "../src/errors.ts";

/**
 * 验收场景：
 *   1. 节点 A 获取锁并启动心跳；
 *   2. 节点 B 排队等待同一把锁；
 *   3. 模拟 A 假死挂起（心跳停止）；
 *   4. 租约在 5 秒后自然超时，B 成功获锁；
 *   5. A 恢复后尝试写操作，被 FenceToken 机制拒绝。
 */
test("验收：A 假死 → 租约 5 秒超时 → B 获锁 → A 恢复写操作被 FenceToken 拒绝", async () => {
  const server = new LockServer();
  const store = new FencedStore();
  const nodeA = new LockClient(server, "node-A");
  const nodeB = new LockClient(server, "node-B");
  const TTL = 5000;

  // 1. A 获取锁并启动心跳，随后正常写入
  const handleA = await nodeA.acquire("shared-resource", {
    ttlMs: TTL,
    heartbeatIntervalMs: 1000,
  });
  assert.equal(handleA.fenceToken, 1);
  assert.ok(handleA.isHeld);
  store.write(handleA.fenceToken, "balance", "A:100");
  assert.equal(store.read("balance"), "A:100");

  // 2. B 排队等待同一把锁
  let bGranted = false;
  const bPromise = nodeB
    .acquire("shared-resource", { ttlMs: TTL })
    .then((h) => {
      bGranted = true;
      return h;
    });
  await sleep(100);
  assert.equal(server.waiterCount("shared-resource"), 1);

  // 3. A 假死挂起：心跳停止（此刻租约尚未被任何续期延长，5 秒后自然过期）
  const expectedExpiry = handleA.expiresAt;
  handleA.suspend();

  // 4a. 租约到期前，B 绝不能获锁（互斥性）
  await sleep(1000);
  assert.equal(bGranted, false, "租约未过期时 B 不应获锁");

  // 4b. 租约 5 秒自然超时后，B 成功获锁
  const handleB = await bPromise;
  const acquiredAt = Date.now();
  assert.ok(
    acquiredAt >= expectedExpiry - 50,
    `B 只能在租约过期后获锁：expected>=${expectedExpiry}, actual=${acquiredAt}`,
  );
  assert.ok(
    acquiredAt <= expectedExpiry + 1000,
    `B 应在租约过期后很快被唤醒：expected=${expectedExpiry}, actual=${acquiredAt}`,
  );
  assert.ok(handleB.isHeld);
  assert.equal(handleB.fenceToken, 2, "B 获得更大的 fence token");

  // 5a. A 恢复：发现租约已丢失
  let lostErr: unknown = null;
  handleA.on("lost", (err) => {
    lostErr = err;
  });
  handleA.resume();
  assert.equal(handleA.isHeld, false);
  assert.equal(handleA.handleState, "lost");
  assert.ok(lostErr instanceof LeaseLostError);
  assert.throws(() => handleA.ensureHeld(), LeaseLostError);

  // 5b. A 绕过客户端检查、直接携带过期 fence token 写共享资源 → 被拒绝
  assert.throws(
    () => store.write(handleA.fenceToken, "balance", "A:999"),
    StaleFenceTokenError,
  );
  assert.equal(store.read("balance"), "A:100", "僵尸持有者不得修改数据");

  // 5c. B 携带新 fence token 正常写入
  store.write(handleB.fenceToken, "balance", "B:200");
  assert.equal(store.read("balance"), "B:200");

  handleB.release();
  server.close();
});
