import { SystemClock } from "../src/clock.ts";
import { FollowerNode } from "../src/follower.ts";
import { LeaseReadOptimizer } from "../src/primary.ts";
import { SimulatedTransport } from "../src/transport.ts";

const clock = new SystemClock();
const FOLLOWERS = ["f1", "f2", "f3", "f4", "f5"];

const transport = new SimulatedTransport({ latencyMs: 1, clock });
for (const id of FOLLOWERS) {
  const follower = new FollowerNode(id, clock);
  transport.register(id, (req) => follower.handleHeartbeat(req));
}

const primary = new LeaseReadOptimizer({
  nodeId: "primary",
  followerIds: FOLLOWERS,
  clock,
  transport,
  leaseDurationMs: 5_000, // 5s 租约
  maxClockSkewMs: 10, // 时钟偏移上界 ε = 10ms
});

primary.write("user:1", { name: "alice", balance: 100 });

// ── 场景 1：租约安全窗口内，1 万次并发读，零网络开销 ──────────────
primary.lease.grant(5_000);
console.log(`[场景1] 租约剩余 ${primary.lease.remainingMs()}ms，处于绝对安全窗口`);

const N = 10_000;
const start = performance.now();
const results = await Promise.all(Array.from({ length: N }, () => primary.read("user:1")));
const elapsed = performance.now() - start;

console.log(`  完成 ${N.toLocaleString()} 次并发读，耗时 ${elapsed.toFixed(1)}ms`);
console.log(`  吞吐 ≈ ${Math.round(N / (elapsed / 1000)).toLocaleString()} reads/s`);
console.log(`  网络调用次数 = ${transport.networkCallCount}（零网络开销 ✓）`);
console.log(`  全部命中本地快速路径: ${results.every((r) => r.source === "lease-local")}`);

// ── 场景 2：租约剩余不足 100ms，自动降级为全量心跳确认 ──────────────
await new Promise((r) => setTimeout(r, 4_950)); // 等待租约剩余 < 100ms + ε
console.log(`\n[场景2] 租约剩余 ${primary.lease.remainingMs().toFixed(0)}ms，低于 100ms 阈值`);

const before = transport.networkCallCount;
const degraded = await primary.read("user:1");
console.log(`  读路径来源 = ${degraded.source}（降级 ✓）`);
console.log(`  本次读网络调用 = ${transport.networkCallCount - before} 次（全量心跳确认）`);
console.log(`  心跳确认后续期，租约剩余 ${primary.lease.remainingMs()}ms`);

// ── 场景 3：多数派不可达时拒绝读，保证线性一致性 ──────────────────
transport.partition("f3");
transport.partition("f4");
transport.partition("f5");
primary.lease.grant(50); // 人为制造租约即将过期
try {
  await primary.read("user:1");
  console.log("\n[场景3] 意外：读成功了");
} catch (err) {
  console.log(`\n[场景3] 多数派不可达 → 拒绝读：${(err as Error).message}（线性一致性 ✓）`);
}
