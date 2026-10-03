/**
 * 运行样例：模拟"配置中心修改数据库密码 -> 全网换发 -> 脱网慢节点租约自然失效熔断"。
 * 运行：npm run demo
 */
import { ManualClock } from "../src/clock.ts";
import { ConfigCenter } from "../src/config-center.ts";
import { WorkerNode } from "../src/worker-node.ts";

const TTL = 30_000; // 租约 30s
const clock = new ManualClock(Date.now());
const center = new ConfigCenter(
  { "db.host": "db.internal", "db.password": "s3cret-v1" },
  { leaseTtlMs: TTL, clock },
);

const web1 = new WorkerNode("web-1", center, clock);
const web2 = new WorkerNode("web-2", center, clock);
const slow = new WorkerNode("web-slow", center, clock);
for (const node of [web1, web2, slow]) node.connect();

function tryServe(node: WorkerNode, query: string): string {
  try {
    const res = node.serve(query);
    return `OK   v${res.configVersion} ${res.result}`;
  } catch (err) {
    return `DENY ${(err as Error).message}`;
  }
}

console.log("== 1. 初始：三节点租用 v1 配置并接客 ==");
for (const node of [web1, web2, slow]) {
  console.log(`  ${node.nodeId}: ${tryServe(node, "SELECT 1")}`);
}

console.log("\n== 2. web-slow 脱网；配置中心修改数据库密码并全网发布 ==");
slow.disconnect();
center.updateConfig({ "db.password": "s3cret-v2" });
console.log(`  配置中心当前版本: v${center.currentVersion}`);
console.log(`  web-1 生效配置: v${web1.currentConfigVersion}, db.password=${web1.configValue("db.password")}`);
console.log(`  web-2 生效配置: v${web2.currentConfigVersion}, db.password=${web2.configValue("db.password")}`);
console.log(`  web-slow 生效配置: v${slow.currentConfigVersion} (脱网，仍持旧配置)`);

console.log("\n== 3. 租约到期前：旧租约宽限内仍可服务；健康节点续租 ==");
clock.advance(TTL - 1);
web1.renewLease();
web2.renewLease();
console.log(`  web-slow: ${tryServe(slow, "SELECT 1")}`);

console.log("\n== 4. 旧租约自然失效：脱网节点主动熔断，拒绝流量 ==");
clock.advance(1);
console.log(`  web-slow: ${tryServe(slow, "UPDATE account SET balance=...")}`);
console.log(`  web-1: ${tryServe(web1, "UPDATE account SET balance=...")}`);

console.log("\n== 5. web-slow 恢复联网，重新租用 v2 配置后恢复接客 ==");
slow.connect();
console.log(`  web-slow 生效配置: v${slow.currentConfigVersion}, db.password=${slow.configValue("db.password")}`);
console.log(`  web-slow: ${tryServe(slow, "SELECT 1")}`);
