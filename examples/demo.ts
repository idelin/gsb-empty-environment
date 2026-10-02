import { DualRingController } from "../src/index.ts";

const ZONES = ["zone-a", "zone-b", "zone-c"];

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// 1. 启动控制器：6 个节点，跨 3 个可用区
const controller = new DualRingController(
  Array.from({ length: 6 }, (_, i) => ({
    id: `node-${i}`,
    topology: { region: "cn-east", zone: ZONES[i % ZONES.length] },
  })),
);

const keys = Array.from({ length: 5000 }, (_, i) => `user:${i}`);
controller.trackKeys(keys);

line("1. 主环稳态运行（承载全量流量）");
const steady = new Map(keys.map((k) => [k, controller.route(k).nodeId]));
const dist = new Map<string, number>();
for (const nodeId of steady.values()) {
  dist.set(nodeId!, (dist.get(nodeId!) ?? 0) + 1);
}
console.log("节点键分布:", Object.fromEntries([...dist].sort()));

line("2. 向备环注入缩容变更（摘除 node-1, node-4）并预热");
const warmup = controller.injectTopologyChange({ remove: ["node-1", "node-4"] });
console.log(`预热状态: ${warmup.status}, 预热耗时: ${warmup.warmupMs.toFixed(2)}ms`);
console.log(
  `迁移预案: 预计 ${(warmup.plan.estimatedMovedFraction * 100).toFixed(1)}% 哈希空间易主, ` +
    `${warmup.plan.movedKeys}/${warmup.plan.trackedKeys} 个已登记键迁移`,
);
console.log("各节点净增/净减:", warmup.plan.perNodeDelta);

line("3. 预热期间主环流量零扰动验证");
let disturbed = 0;
for (const key of keys) {
  if (controller.route(key).nodeId !== steady.get(key)) disturbed++;
}
console.log(`主环路由发生变化的键: ${disturbed}（期望 0）`);

line("4. 主环异常 → 原子指针翻转");
const failover = controller.failover();
console.log(
  `翻转完成: epoch=${failover.epoch}, 节点数=${failover.nodeCount}, ` +
    `翻转耗时=${failover.flipMs.toFixed(3)}ms`,
);

line("5. 切换后零瞬时路由错乱验证");
const verification = controller.verifyAgainstPlan(warmup.plan);
console.log(
  `校验键数: ${verification.checked}, 路由错乱数: ${verification.mismatches.length}`,
);
let onRemoved = 0;
for (const key of keys) {
  const nodeId = controller.route(key).nodeId;
  if (nodeId === "node-1" || nodeId === "node-4") onRemoved++;
}
console.log(`落在已摘除节点上的键: ${onRemoved}（期望 0）`);
console.log(
  verification.mismatches.length === 0 && onRemoved === 0
    ? "✔ 流量瞬时平滑过渡，零瞬时路由错乱"
    : "✘ 检测到路由错乱",
);
