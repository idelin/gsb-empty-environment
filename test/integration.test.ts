import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { MasterCoordinator } from "../src/master.ts";
import { InMemoryTransport } from "../src/transport.ts";
import { WorkerNode } from "../src/worker.ts";

/** 与 src/demo.ts 相同的突发消费驱动 */
async function burst(w: WorkerNode, max: number): Promise<number> {
  let consumed = 0;
  let emptyRounds = 0;
  while (consumed < max && emptyRounds < 2) {
    if (w.tryConsume()) {
      consumed += 1;
      emptyRounds = 0;
      continue;
    }
    await w.settle();
    if (w.localTokens === 0) {
      await w.sync();
      if (w.localTokens === 0) emptyRounds += 1;
    }
  }
  return consumed;
}

test("验收：3 Worker 平分 3000 QPS，A 突发耗尽后从空闲的 C 批量调拨", async () => {
  const clock = new ManualClock(0);
  const master = new MasterCoordinator({ pool: 3000, windowMs: 1000, clock });
  const transport = new InMemoryTransport(master);
  const make = (id: string) =>
    new WorkerNode({ id, transport, clock, batchSize: 1000, renewThreshold: 100 });
  const a = make("A");
  const b = make("B");
  const c = make("C");

  // 1) 三节点注册，平分 3000 QPS
  await Promise.all([a.sync(), b.sync(), c.sync()]);
  assert.equal(master.allocatedOf("A"), 1000);
  assert.equal(master.allocatedOf("B"), 1000);
  assert.equal(master.allocatedOf("C"), 1000);
  assert.equal(master.poolFree, 0);

  // 2) B 半忙（用 500 并上报），C 完全空闲
  for (let i = 0; i < 500; i += 1) assert.ok(b.tryConsume());
  await b.report();

  // 3) A 突发流量：耗尽自身 1000 后，批量心跳触发调拨
  const consumed = await burst(a, 5000);
  assert.equal(consumed, 2500);

  // 调拨路径：先掏空空闲的 C（1000），再调 B 的未使用余额（500）
  assert.deepEqual(master.reclaims, [
    { from: "C", to: "A", tokens: 1000, at: 0 },
    { from: "B", to: "A", tokens: 500, at: 0 },
  ]);
  assert.equal(master.allocatedOf("A"), 2500);
  assert.equal(master.allocatedOf("B"), 500);
  assert.equal(master.allocatedOf("C"), 0);
  assert.equal(master.totalAllocated, 3000); // 总量守恒，不超发

  // C 下次心跳时通过权威 allocated 收敛到 0
  await c.report();
  assert.equal(c.localTokens, 0);
  assert.equal(c.tryConsume(), false);

  // 4) 批量心跳显著降低 RPC：2500 次消费仅个位数 RPC
  assert.ok(transport.rpcCount <= 12, `rpcCount=${transport.rpcCount}`);

  // 5) 窗口滑动后配额池重置，三节点重新各得 1000
  clock.advance(1000);
  await Promise.all([a.sync(), b.sync(), c.sync()]);
  assert.equal(master.allocatedOf("A"), 1000);
  assert.equal(master.allocatedOf("B"), 1000);
  assert.equal(master.allocatedOf("C"), 1000);
});
