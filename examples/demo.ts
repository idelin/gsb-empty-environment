import { LoadAwareDistributor } from "../src/distributor.ts";

const KEY_COUNT = 100_000;
const keys = Array.from({ length: KEY_COUNT }, (_, i) => `user-${i}`);

function report(title: string, d: LoadAwareDistributor): Map<string, string> {
  const counts = d.distribute(keys);
  console.log(`\n=== ${title} ===`);
  console.log(`虚拟节点: A=${d.ring.getVNodeCount("A")} B=${d.ring.getVNodeCount("B")} C=${d.ring.getVNodeCount("C")}`);
  const assignment = new Map<string, string>();
  for (const k of keys) assignment.set(k, d.route(k));
  for (const id of ["A", "B", "C"]) {
    const n = counts.get(id) ?? 0;
    const pct = ((n / KEY_COUNT) * 100).toFixed(1).padStart(5);
    console.log(`  节点 ${id}: ${String(n).padStart(6)} 请求 (${pct}%) ${"█".repeat(Math.round((n / KEY_COUNT) * 50))}`);
  }
  return assignment;
}

const d = new LoadAwareDistributor();
d.addNode("A", { cpu: 0.3, memory: 0.3 });
d.addNode("B", { cpu: 0.3, memory: 0.3 });
d.addNode("C", { cpu: 0.3, memory: 0.3 });

const before = report("基线：三节点 CPU 均为 30%，等权分流", d);

console.log("\n>>> 监控告警：节点 A CPU 从 30% 骤升至 90% <<<");
d.updateLoad("A", { cpu: 0.9, memory: 0.3 });
const weights = d.cycle();
console.log(`容量感知器调控完成，生效权重: A=${weights.get("A")} B=${weights.get("B")} C=${weights.get("C")}`);

const after = report("泻洪后：A 虚拟节点收缩，流量自动分流至 B/C", d);

let movedFromA = 0;
let bcStable = 0;
let bcTotal = 0;
for (const k of keys) {
  if (before.get(k) === "A" && after.get(k) !== "A") movedFromA++;
  if (before.get(k) !== "A") {
    bcTotal++;
    if (after.get(k) === before.get(k)) bcStable++;
  }
}
console.log(`\nA 溢出并迁移的请求: ${movedFromA}`);
console.log(`B/C 上保持不动的键: ${bcStable}/${bcTotal}（最小扰动，无全局重哈希，避免雪崩）`);
