import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeClock } from "../src/clock.ts";
import { MasterCoordinator } from "../src/master.ts";
import { WorkerNode } from "../src/worker.ts";

function setup(totalQps = 3000) {
  const clock = new FakeClock();
  const master = new MasterCoordinator({ totalQps, windowMs: 1000, clock });
  return { clock, master };
}

test("Worker：本地配额内直接放行，不产生额外 RPC", async () => {
  const { clock, master } = setup();
  const worker = new WorkerNode({ id: "A", master, clock, batchSize: 1000 });
  await worker.start();
  assert.equal(master.rpcCount, 1); // 仅初始心跳

  for (let i = 0; i < 500; i += 1) {
    assert.equal(worker.tryAcquire(), true);
  }
  await worker.flush();
  assert.equal(master.rpcCount, 1); // 500 个请求 0 次额外 RPC
  assert.equal(worker.stats().served, 500);
});

test("Worker：余量低于阈值时触发异步批量续订", async () => {
  const { clock, master } = setup();
  const worker = new WorkerNode({ id: "A", master, clock, batchSize: 1000 });
  await worker.start();

  for (let i = 0; i < 800; i += 1) worker.tryAcquire(); // 余量降至 200 = 阈值
  await worker.flush();

  assert.equal(master.rpcCount, 2); // 恰好 1 次批量续订
  assert.equal(worker.stats().localTokens, 1200); // 200 余量 + 1000 批量补货
});

test("Worker：本地耗尽且续订未到账时拒绝请求", async () => {
  const { clock, master } = setup(1000); // 池子只有 1000
  const worker = new WorkerNode({ id: "A", master, clock, batchSize: 1000 });
  await worker.start();

  let served = 0;
  while (worker.tryAcquire()) served += 1;
  await worker.flush();
  assert.equal(served, 1000);
  assert.equal(worker.tryAcquire(), false); // 池子已空，续订拿不到新额度
  assert.ok(worker.stats().rejected >= 1);
});

test("Worker：租约过期后本地余量作废，需重新续订", async () => {
  const { clock, master } = setup();
  const worker = new WorkerNode({ id: "A", master, clock, batchSize: 1000 });
  await worker.start();
  assert.equal(worker.tryAcquire(), true);

  clock.advance(1001); // 租约过期
  assert.equal(worker.tryAcquire(), false); // 本地余量作废，先拒一次并触发续订
  // 窗口已滑过、池子恢复，续订到账后恢复放行
  assert.equal(await worker.acquire(), true);
  assert.ok(worker.stats().heartbeats >= 2);
});
