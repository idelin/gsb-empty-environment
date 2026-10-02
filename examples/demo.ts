/**
 * 运行样例：模拟两个分布式节点并发操作后合并。
 * 运行：npm run demo
 */
import { AWLWWMap } from "../src/awlwwmap.ts";

function show(label: string, m: AWLWWMap): void {
  console.log(`  [${label}] keys=${JSON.stringify(m.keysLive())} user_profile=${JSON.stringify(m.get("user_profile"))}`);
}

// ---- 初始：节点 A 写入用户资料，并同步给节点 B ----
const nodeA = new AWLWWMap("A");
nodeA.set("user_profile", "name", "alice");
nodeA.set("user_profile", "age", 20);

const nodeB = nodeA.clone("B"); // 模拟网络同步：B 获得一致快照
console.log("同步后初始状态：");
show("A", nodeA);
show("B", nodeB);

// ---- 并发分叉：A 删除根键，B 同时更新键内字段 ----
console.log("\n并发操作：A 删除 user_profile ；B 设置 user_profile.age = 25");
nodeA.removeKey("user_profile");
nodeB.set("user_profile", "age", 25);
show("A", nodeA);
show("B", nodeB);

// ---- 因果合并：Add-Wins 判定更新优先 ----
nodeA.merge(nodeB);
nodeB.merge(nodeA.clone());
console.log("\n合并后（两节点收敛一致）：");
show("A", nodeA);
show("B", nodeB);

console.log(
  "\n结论：并发『删除键』与『更新键内字段』竞态中，更新优先 —— " +
  "user_profile 被保留且 age=25；被 A 实际观测到的旧字段 name 不复活。"
);
