import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeClock } from "../src/clock.ts";
import { MasterCoordinator } from "../src/master.ts";
import { WorkerNode } from "../src/worker.ts";

/**
 * 验收场景：
 * - 3 个 Worker 平分 3000 QPS 限流池（各 1000）；
 * - Worker A 突发流量迅速耗尽自身 1000 配额；
 * - 批量心跳续订时，Master 从空闲的 Worker C 调拨空余额度给 A；
 * - 全程满足全局不变式：窗口内消耗 + 未消耗租约 ≤ 3000。
 */
test("验收：A 突发耗尽配额，批量心跳从空闲的 C 调拨额度", async () => {
  const clock = new FakeClock();
  const master = new MasterCoordinator({ totalQps: 3000, windowMs: 1000, clock });
  const a = new WorkerNode({ id: "A", master, clock, batchSize: 1000 });
  const b = new WorkerNode({ id: "B", master, clock, batchSize: 1000 });
  const c = new WorkerNode({ id: "C", master, clock, batchSize: 1000 });
  await Promise.all([a.start(), b.start(), c.start()]);

  // 1) 初始平分：各 1000，池子恰好分完
  let snap = master.snapshot();
  assert.deepEqual(
    snap.workers.map((w) => [w.id, w.allocation]),
    [["A", 1000], ["B", 1000], ["C", 1000]],
  );
  assert.equal(snap.available, 0);

  // 2) B 以 200 QPS 涓流运行并通过保活心跳上报；C 完全空闲仅保活
  for (let i = 0; i < 200; i += 1) assert.ok(b.tryAcquire());
  await b.keepalive();
  await c.keepalive();

  // 3) A 突发：本地 1000 配额迅速耗尽，阈值触发异步批量续订
  let burst = 0;
  while (a.tryAcquire()) burst += 1;
  await a.flush();
  assert.ok(burst >= 2000, `A 应借到 C 的额度继续放行，实际 ${burst}`);

  // 4) 调拨记录：先从最空闲的 C 调拨 1000，C 的租约被清空
  const fromC = master.reallocations.filter((r) => r.from === "C" && r.to === "A");
  assert.equal(fromC.length, 1);
  assert.equal(fromC[0].amount, 1000);
  snap = master.snapshot();
  assert.equal(snap.workers.find((w) => w.id === "C")!.allocation, 0);

  // 5) C 耗尽后，A 的下一批心跳继续从低负载的 B 调拨其空闲部分
  assert.ok(await a.acquire(), "A 应能凭调拨额度继续放行");
  while (a.tryAcquire()) burst += 1;
  await a.flush();
  const fromB = master.reallocations.filter((r) => r.from === "B" && r.to === "A");
  assert.equal(fromB.length, 1, "应继续从 B 调拨空闲额度");
  assert.equal(fromB[0].amount, 600);
  assert.ok(
    fromB.reduce((sum, r) => sum + r.amount, 0) <= 600,
    "B 自身消耗 200，最多被回收 600 空闲额度",
  );

  // 6) 全局不变式：窗口内已消耗 + 未消耗租约 ≤ 3000
  snap = master.snapshot();
  const outstanding = snap.workers.reduce((sum, w) => sum + w.allocation, 0);
  assert.ok(
    snap.consumedInWindow + outstanding <= 3000,
    `不变式被破坏: consumed=${snap.consumedInWindow} outstanding=${outstanding}`,
  );

  // 7) 池子真正耗尽后，A 被限流拒绝
  assert.equal(a.tryAcquire(), false);

  // 8) RPC 批量化：A 放行 2000+ 请求，心跳次数仅为个位数
  const statsA = a.stats();
  assert.ok(statsA.served >= 2000);
  assert.ok(statsA.heartbeats <= 5, `心跳次数应远小于请求数，实际 ${statsA.heartbeats}`);

  // 9) 窗口滑过后配额回收，A 可再次获取
  clock.advance(1100);
  assert.ok(await a.acquire(), "窗口重置后 A 应能重新获得配额");
});
