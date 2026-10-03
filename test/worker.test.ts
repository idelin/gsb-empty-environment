import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { MasterCoordinator } from "../src/master.ts";
import { InMemoryTransport } from "../src/transport.ts";
import { WorkerNode } from "../src/worker.ts";

function setup(pool: number, batchSize = 1000, renewThreshold = 100) {
  const clock = new ManualClock();
  const master = new MasterCoordinator({ pool, windowMs: 1000, clock });
  const transport = new InMemoryTransport(master);
  const worker = new WorkerNode({ id: "W", transport, clock, batchSize, renewThreshold });
  return { clock, master, transport, worker };
}

test("本地消耗 Local Token 不产生额外 RPC", async () => {
  const { transport, worker } = setup(3000);
  await worker.sync();
  assert.equal(transport.rpcCount, 1);

  for (let i = 0; i < 500; i += 1) {
    assert.ok(worker.tryConsume());
  }
  assert.equal(worker.localTokens, 500);
  assert.equal(transport.rpcCount, 1);
});

test("余额低于阈值时异步批量续订，RPC 次数远小于消费次数", async () => {
  const { transport, worker } = setup(3000);
  await worker.sync();

  let consumed = 0;
  while (worker.tryConsume()) consumed += 1;
  assert.equal(consumed, 1000);

  await worker.settle();
  assert.equal(worker.localTokens, 1000);
  assert.equal(worker.allocated, 2000);
  assert.equal(transport.rpcCount, 2);
});

test("Master 无额度可授时 tryConsume 快速失败", async () => {
  const { transport, worker } = setup(100, 100, 10);
  await worker.sync();

  let consumed = 0;
  while (worker.tryConsume()) consumed += 1;
  assert.equal(consumed, 100);

  await worker.settle();
  assert.equal(worker.localTokens, 0);
  assert.equal(worker.tryConsume(), false);
  await worker.settle();
  assert.ok(transport.rpcCount <= 4);
});

test("租约随窗口过期后，本地余额作废并重新批量注册", async () => {
  const { clock, transport, worker } = setup(3000);
  await worker.sync();
  for (let i = 0; i < 300; i += 1) worker.tryConsume();
  const rpcBefore = transport.rpcCount;

  clock.advance(1000);
  assert.equal(worker.localTokens, 700); // 未触碰前仍是旧值
  assert.equal(worker.tryConsume(), false); // 触发过期检查，余额作废

  await worker.settle();
  assert.equal(worker.localTokens, 1000); // 新窗口重新预分配
  assert.equal(worker.usedTotal, 0);
  assert.ok(transport.rpcCount > rpcBefore);
});
