import { setTimeout as sleep } from "node:timers/promises";
import { MasterCoordinator, type PoolSnapshot } from "./master.ts";
import { WorkerNode } from "./worker.ts";

const TOTAL_QPS = 3000;
const WINDOW_MS = 1000;

function printSnapshot(title: string, snap: PoolSnapshot): void {
  console.log(`\n=== ${title} ===`);
  console.log("Worker | 未消耗租约 | 窗口内已用 | 租约剩余TTL(ms)");
  console.log("-------|-----------|-----------|----------------");
  for (const w of snap.workers) {
    console.log(
      `  ${w.id}    | ${String(w.allocation).padStart(9)} | ${String(w.usageInWindow).padStart(9)} | ${String(w.leaseExpiresInMs).padStart(14)}`,
    );
  }
  console.log(
    `池子余量 available=${snap.available}  窗口内已消耗 consumed=${snap.consumedInWindow}  总量=${snap.totalQps}`,
  );
}

function assertInvariant(snap: PoolSnapshot): void {
  const outstanding = snap.workers.reduce((sum, w) => sum + w.allocation, 0);
  if (snap.consumedInWindow + outstanding > snap.totalQps) {
    console.error(
      `✗ 全局不变式被破坏: consumed(${snap.consumedInWindow}) + outstanding(${outstanding}) > ${snap.totalQps}`,
    );
    process.exit(1);
  }
  console.log(
    `✓ 全局不变式成立: consumed(${snap.consumedInWindow}) + outstanding(${outstanding}) = ${snap.consumedInWindow + outstanding} ≤ ${snap.totalQps}`,
  );
}

const master = new MasterCoordinator({ totalQps: TOTAL_QPS, windowMs: WINDOW_MS });
const a = new WorkerNode({ id: "A", master, batchSize: 1000 });
const b = new WorkerNode({ id: "B", master, batchSize: 1000 });
const c = new WorkerNode({ id: "C", master, batchSize: 1000 });

console.log("分布式滑动窗口限流：心跳租约 + 配额预分配 演示");
console.log(`限流池 ${TOTAL_QPS} QPS，窗口 ${WINDOW_MS}ms，3 个 Worker 各预分配 1000`);

// 阶段 1：启动，平分配额
await Promise.all([a.start(), b.start(), c.start()]);
printSnapshot("阶段 1：初始平分 3000 QPS", master.snapshot());
assertInvariant(master.snapshot());

// 阶段 2：B 涓流 200 QPS 并保活上报；C 空闲仅保活
for (let i = 0; i < 200; i += 1) b.tryAcquire();
await b.keepalive();
await c.keepalive();
printSnapshot("阶段 2：B 涓流 200，C 空闲", master.snapshot());

// 阶段 3：A 突发流量，迅速耗尽自身 1000 配额并触发批量续订
console.log("\n=== 阶段 3：Worker A 突发流量 ===");
let burst = 0;
while (await a.acquire()) burst += 1;
console.log(`A 本轮突发共放行 ${burst} 个请求，心跳 RPC 仅 ${a.stats().heartbeats} 次`);
console.log("额度调拨记录：");
for (const r of master.reallocations) {
  console.log(`  Master 从空闲的 Worker ${r.from} 调拨 ${r.amount} → Worker ${r.to}`);
}
printSnapshot("突发后的配额分布", master.snapshot());
assertInvariant(master.snapshot());
console.log(`A 继续请求被限流拒绝: tryAcquire() = ${a.tryAcquire()}`);

// 阶段 4：窗口滑过，配额回收，A 可再次获取
console.log(`\n=== 阶段 4：休眠 ${WINDOW_MS + 100}ms 等待滑动窗口重置 ===`);
await sleep(WINDOW_MS + 100);
const ok = await a.acquire();
console.log(`窗口重置后 A 重新获取配额: acquire() = ${ok}`);
printSnapshot("窗口重置后的配额分布", master.snapshot());
assertInvariant(master.snapshot());

// 汇总
console.log("\n=== 汇总 ===");
for (const w of [a, b, c]) {
  const s = w.stats();
  console.log(
    `Worker ${s.id}: 放行 ${s.served}，拒绝 ${s.rejected}，心跳 ${s.heartbeats} 次`,
  );
}
console.log(`Master 共处理 heartbeat RPC ${master.rpcCount} 次`);
console.log("✓ 演示完成");
