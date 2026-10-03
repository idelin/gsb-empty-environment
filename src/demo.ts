import { systemClock } from "./clock.ts";
import { LeaseReadOptimizer } from "./primary.ts";
import { FollowerReplica } from "./follower.ts";

/**
 * 运行样例：5 副本集群，1 万次并发读。
 * 1) 租约安全窗口内：零网络本地读，测吞吐；
 * 2) 等待租约剩余 < 100ms：自动降级为全量心跳确认。
 */
const SAFE_WINDOW_MS = 100;
const followers = Array.from({ length: 5 }, (_, i) => new FollowerReplica(`follower-${i}`, 1));
const primary = new LeaseReadOptimizer(
  { ttlMs: 1_500, safeWindowMs: SAFE_WINDOW_MS, clockSkewBoundMs: 20 },
  followers,
  systemClock,
);

primary.write("user:42", { name: "alice", balance: 1000 });
await primary.acquireLease();
console.log(`租约已获取，剩余有效窗口 ${primary.leaseRemainingMs()}ms（安全下界 ${SAFE_WINDOW_MS}ms）\n`);

// ---- 阶段 1：1 万次并发读，安全窗口内零网络 ----
const heartbeatsBefore = followers.map((f) => f.getHeartbeatCount());
const start = performance.now();
const results = await Promise.all(Array.from({ length: 10_000 }, () => primary.read("user:42")));
const elapsed = performance.now() - start;

const allFast = results.every((r) => r.path === "fast");
const heartbeatsAfter = followers.map((f) => f.getHeartbeatCount());
const networkDuringReads = heartbeatsAfter.reduce((a, b, i) => a + (b - heartbeatsBefore[i]), 0);

console.log("== 阶段 1：10,000 次并发读（租约安全窗口内） ==");
console.log(`  全部走快速路径（本地读）: ${allFast}`);
console.log(`  读期间网络心跳数: ${networkDuringReads}（零网络开销）`);
console.log(`  总耗时: ${elapsed.toFixed(1)}ms，吞吐: ${Math.round(10_000 / (elapsed / 1000)).toLocaleString()} reads/s\n`);

// ---- 阶段 2：等待租约剩余 < 100ms，验证自动降级 ----
const waitMs = primary.leaseRemainingMs() - SAFE_WINDOW_MS + 5;
console.log(`== 阶段 2：等待 ${Math.round(waitMs)}ms 使剩余租约不足 ${SAFE_WINDOW_MS}ms ==`);
await new Promise((r) => setTimeout(r, waitMs));
console.log(`  当前剩余租约: ${Math.max(0, Math.round(primary.leaseRemainingMs()))}ms`);

const degraded = await primary.read("user:42");
const stats = primary.getStats();
console.log(`  读路径: ${degraded.path}（自动降级为全量心跳确认）`);
console.log(`  统计: fast=${stats.fastPathReads}, degraded=${stats.degradedReads}, 网络协商轮次=${stats.networkRounds}`);
console.log(`  降级后续租成功，剩余租约恢复至: ${Math.round(primary.leaseRemainingMs())}ms`);
