import { ManualClock } from "./clock.ts";
import { MasterCoordinator } from "./master.ts";
import { InMemoryTransport } from "./transport.ts";
import { WorkerNode } from "./worker.ts";

/**
 * 验收场景复现：
 *   3 个 Worker 平分 3000 QPS 限流池（各预分配 1000）；
 *   Worker B 半忙（用 500），Worker C 完全空闲；
 *   Worker A 突发流量耗尽自身 1000 配额后，
 *   通过批量心跳从空闲的 Worker C（以及 B 的剩余空闲部分）调拨额度。
 */

const clock = new ManualClock(0);
const master = new MasterCoordinator({
  pool: 3000,
  windowMs: 1000,
  clock,
  logger: (line) => console.log(line),
});
const transport = new InMemoryTransport(master);

const makeWorker = (id: string) =>
  new WorkerNode({ id, transport, clock, batchSize: 1000, renewThreshold: 100 });

const workerA = makeWorker("worker-A");
const workerB = makeWorker("worker-B");
const workerC = makeWorker("worker-C");
const workers = [workerA, workerB, workerC];

function printState(): void {
  for (const w of workers) {
    console.log(
      `  ${w.id}: 持有配额=${master.allocatedOf(w.id)} 已用=${master.usedOf(w.id)} 本地余额=${w.localTokens}`,
    );
  }
  console.log(`  池剩余=${master.poolFree}  RPC 累计=${transport.rpcCount}`);
}

/** 突发消费：本地余额耗尽时等待异步续订，Master 再无额度可调拨时停止 */
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

console.log("=== Phase 1: 注册，3 个 Worker 平分 3000 QPS 配额池 ===");
await Promise.all([workerA.sync(), workerB.sync(), workerC.sync()]);
printState();

console.log("\n=== Phase 2: Worker B 消耗 500 并上报用量，Worker C 保持空闲 ===");
for (let i = 0; i < 500; i += 1) workerB.tryConsume();
await workerB.report();
printState();

console.log("\n=== Phase 3: Worker A 突发流量，耗尽自身配额后触发批量续订/调拨 ===");
const consumed = await burst(workerA, 5000);
console.log(`worker-A 突发共消费 ${consumed} tokens（自身初始配额仅 1000）`);
printState();
await workerC.report();
console.log(`worker-C 下一次心跳后收敛：本地余额=${workerC.localTokens}（被调拨的额度自动失效）`);

console.log("\n=== Phase 4: 滑动窗口推进 1s，租约过期，配额池重置 ===");
clock.advance(1000);
await Promise.all([workerA.sync(), workerB.sync(), workerC.sync()]);
printState();

console.log(`\n总计：worker-A 消费 ${consumed} tokens，全程 RPC 仅 ${transport.rpcCount} 次`);
