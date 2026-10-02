/**
 * 运行样例：集群动态配置分发与生效机制
 *   node --experimental-strip-types examples/demo.ts
 *
 * 场景：3 个节点在线 → 配置中心修改数据库密码并发布 →
 *       node-c 恰好脱网 → 旧租约自然失效后主动停止接客。
 */
import { ConfigCenter } from "../src/config-center.ts";
import { NoValidLeaseError, WorkerNode } from "../src/worker-node.ts";

const LEASE_TTL_MS = 600;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

function tryServe(node: WorkerNode, label: string): void {
  try {
    const res = node.serve(label);
    console.log(
      `  [${label}] ${res.nodeId} 接客成功  configVersion=v${res.configVersion}  dbPassword=${res.dbPassword}`,
    );
  } catch (err) {
    if (err instanceof NoValidLeaseError) {
      console.log(`  [${label}] ${node.nodeId} 拒绝接客: ${err.message}`);
    } else {
      throw err;
    }
  }
}

const center = new ConfigCenter({
  leaseTtlMs: LEASE_TTL_MS,
  initialConfig: { dbPassword: "pw-v1" },
});

const nodes = ["node-a", "node-b", "node-c"].map(
  (id) => new WorkerNode(id, center, { renewalMarginMs: 200 }),
);

console.log(`== 1. 节点上线，租用配置 v1（租约 TTL=${LEASE_TTL_MS}ms）==`);
for (const node of nodes) {
  node.start();
  node.startAutoMaintain(100);
  tryServe(node, "req");
}

console.log("\n== 2. node-c 脱网（收不到推送、无法续租）==");
const nodeC = nodes[2];
nodeC.disconnect();
console.log("  node-c 已脱网");

console.log("\n== 3. 配置中心修改数据库密码并发布 ==");
const result = center.updateConfig({ dbPassword: "pw-v2" });
console.log(
  `  发布 v${result.version}：撤销旧租约 ${result.revokedLeases} 张，` +
    `推送成功 [${result.delivered}]，未送达 [${result.undelivered}]`,
);
for (const node of nodes) tryServe(node, "req");

console.log("\n== 4. 等待 node-c 旧租约自然失效（TTL 到期）==");
await sleep(LEASE_TTL_MS + 150);
nodeC.maintain();
for (const node of nodes) tryServe(node, "req");

console.log("\n== 5. node-c 恢复网络，重新租用最新配置 ==");
nodeC.stop();
nodeC.start();
nodeC.startAutoMaintain(100);
for (const node of nodes) tryServe(node, "req");

for (const node of nodes) node.stop();
console.log("\n演示结束：脱网节点在租约失效后主动停服，未使用过期配置操作脏数据。");
