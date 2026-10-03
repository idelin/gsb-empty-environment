/**
 * 运行样例：双层层次化哈希聚合路由网关
 * 运行：node examples/demo.ts   （需要 Node.js >= 23.6，原生运行 TypeScript）
 */
import { HierarchicalGateway } from "../src/gateway.ts";

const gw = new HierarchicalGateway();

// 第一层：集群网关节点
for (const id of ["gw-east-1", "gw-east-2", "gw-west-1"]) gw.addClusterGateway(id);
// 第二层：各租户独立的协议处理节点环
for (const id of ["t100-p1", "t100-p2", "t100-p3"]) gw.addProtocolNode("100", id);
for (const id of ["t200-p1", "t200-p2"]) gw.addProtocolNode("200", id);

console.log("=== 1. 双层级联穿透：Tenant=100, Device=ABC ===");
const r = gw.route("100", "ABC");
console.log(`请求: Tenant=${r.tenantId}, Device=${r.deviceMac}`);
for (const hop of r.path) {
  console.log(`  [${hop.layer}] ringKey=${hop.ringKey} -> ${hop.nodeId}`);
}
console.log(`结果: 集群网关=${r.clusterGateway}, 协议节点=${r.protocolNode}\n`);

console.log("=== 2. 单租户扩容：租户100 新增 t100-p4 ===");
const devices = Array.from({ length: 1000 }, (_, i) => `DEV-${i}`);
const snap = (t: string) => new Map(devices.map((d) => [d, gw.routeProtocolNode(t, d)]));
const t100Before = snap("100");
const t200Before = snap("200");

gw.addProtocolNode("100", "t100-p4");

let moved100 = 0;
let moved200 = 0;
for (const d of devices) {
  if (gw.routeProtocolNode("100", d) !== t100Before.get(d)) moved100++;
  if (gw.routeProtocolNode("200", d) !== t200Before.get(d)) moved200++;
}
console.log(`租户100 迁移设备: ${moved100}/1000（仅流向新节点 t100-p4）`);
console.log(`租户200 迁移设备: ${moved200}/1000（必须为 0，分布不受影响）\n`);

console.log("=== 3. 容灾：摘除租户100 的 t100-p2 ===");
gw.removeProtocolNode("100", "t100-p2");
const r2 = gw.route("100", "ABC");
console.log(`Tenant=100/Device=ABC 重新路由 -> ${r2.clusterGateway} -> ${r2.protocolNode}`);
console.log(`租户100 存活协议节点: ${gw.protocolNodesOf("100").join(", ")}`);
