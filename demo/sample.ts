/**
 * 运行样例：5 节点集群 → 选举 → 2/3 对称分区 → 多数派续租服务、少数派剥离主权限。
 * 运行：node demo/sample.ts   （需要 Node.js >= 23.6，原生运行 TypeScript）
 */
import { Cluster } from "../src/cluster.ts";

const LEASE_MS = 1000;
const c = new Cluster(["n1", "n2", "n3", "n4", "n5"], { leaseDurationMs: LEASE_MS });

function status(label: string): void {
  const leaders = c.activeLeaders();
  console.log(
    `[t=${String(c.now()).padStart(4)}ms] ${label}\n         leaders=[${leaders.join(", ") || "-"}]`,
  );
}

function tryWrite(id: string, value: string): void {
  try {
    c.node(id).write(value);
    console.log(`         WRITE ${id} "${value}" -> OK`);
  } catch (err) {
    console.log(`         WRITE ${id} "${value}" -> REJECTED (${(err as Error).name})`);
  }
}

console.log(`=== 多数派租约仲裁演示（5 节点，Quorum=${c.quorumSize()}，租约 ${LEASE_MS}ms）===\n`);

console.log("--- 阶段 1：全连通，n1 竞选 ---");
console.log(`         n1.campaign() -> ${c.node("n1").campaign(LEASE_MS)}`);
status("n1 获得 5 票授权，当选 Leader");
tryWrite("n1", "order#1");

console.log("\n--- 阶段 2：对称分区 {n1,n2} | {n3,n4,n5} ---");
c.partitionInto([
  ["n1", "n2"],
  ["n3", "n4", "n5"],
]);
console.log(`         n1.renewLease() -> ${c.node("n1").renewLease(LEASE_MS)} (少数派仅 2 票 < Quorum=3)`);
console.log(`         n3.campaign()   -> ${c.node("n3").campaign(LEASE_MS)} (旧租约未过期，仲裁者拒绝重复授权)`);
status("分区瞬间：n1 租约尚未过期，集群短暂无主可写窗口开始");

console.log("\n--- 阶段 3：推进时钟，租约过期 ---");
c.advance(LEASE_MS);
status("n1 租约过期，自动剥离主权限（本地时钟驱动，无需网络）");
tryWrite("n1", "stale-write");

console.log("\n--- 阶段 4：多数派分区接管并持续服务 ---");
console.log(`         n3.campaign() -> ${c.node("n3").campaign(LEASE_MS)} (3 票凑齐 Quorum)`);
tryWrite("n3", "order#2");
c.advance(LEASE_MS / 2);
console.log(`         n3.renewLease() -> ${c.node("n3").renewLease(LEASE_MS)} (多数派续租成功)`);
c.advance(LEASE_MS / 2);
tryWrite("n3", "order#3");
status("多数派分区持续服务");

console.log("\n--- 阶段 5：分区恢复，n1 重新入列 ---");
c.heal();
console.log(`         n1.isLeader = ${c.node("n1").isLeader}（不会死灰复燃）`);
tryWrite("n3", "order#4");
status("集群恢复全互联，仍只有唯一 Leader");

console.log(`\n=== 结果：n3 已提交日志 = ${JSON.stringify(c.node("n3").log)}，全程无脑裂 ===`);
