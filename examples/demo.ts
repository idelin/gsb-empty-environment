import { Planner, greedyPlan, PlanResult, Problem } from "../src";

/**
 * 场景：燃料(fuel)是全局关键资源，零件(parts)相对独立。
 * quick-patch 便宜又“离目标近”，但会烧掉 deploy-alpha/beta 所需的燃料；
 * careful-patch 贵一点，却走零件通道。贪心会被 quick-patch 诱入死端。
 */
const problem: Problem = {
  resources: [
    { name: "fuel", capacity: 8 },
    { name: "parts", capacity: 4 },
  ],
  goals: [
    { name: "G1", profit: 10 },
    { name: "G2", profit: 10 },
    { name: "G3", profit: 6 },
  ],
  actions: [
    { name: "deploy-alpha", cost: 3, consumes: { fuel: 4 }, achieves: ["G1"] },
    { name: "deploy-beta", cost: 3, consumes: { fuel: 4 }, achieves: ["G2"] },
    { name: "quick-patch", cost: 1, consumes: { fuel: 3 }, achieves: ["G3"] },
    { name: "careful-patch", cost: 4, consumes: { parts: 3 }, achieves: ["G3"] },
  ],
};

function printPlan(title: string, r: PlanResult, initial: Record<string, number>): void {
  console.log(`\n=== ${title} ===`);
  console.log(`初始余额: ${fmt(initial)}`);
  r.steps.forEach((s, i) => {
    console.log(
      `  步骤 ${i + 1}: ${s.action} (成本 ${s.cost})` +
        ` 达成 [${s.achievedNow.join(", ")}] -> 余额 ${fmt(s.balance)}`,
    );
  });
  console.log(`总收益: ${r.profit}  总成本: ${r.cost}  最终余额: ${fmt(r.finalBalance)}`);
}

function fmt(b: Record<string, number>): string {
  return Object.entries(b)
    .map(([k, v]) => `${k}=${v}`)
    .join(", ");
}

const initial: Record<string, number> = {};
for (const r of problem.resources) initial[r.name] = r.capacity;

const planner = new Planner(problem);
const optimal = planner.plan();
printPlan("最优计划（DP 全局搜索）", optimal, initial);
console.log(`探索状态数: ${optimal.statesExplored}`);

const greedy = greedyPlan(problem);
printPlan("贪心基线（按目标距离，仅对比用）", greedy, initial);

console.log("\n=== 死端分析 ===");
const deadEnds = planner.analyzeDeadEnds();
for (const d of deadEnds.slice(0, 5)) {
  console.log(`\n路径: ${d.path.length ? d.path.join(" -> ") : "(初始状态)"}`);
  console.log(`  余额: ${fmt(d.balance)}  未达成: [${d.remainingGoals.join(", ")}]`);
  for (const b of d.blockedGoals) {
    const crit = b.criticalResources
      .map((c) => `${c.resource} 需 ${c.need} 仅剩 ${c.have}`)
      .join("; ");
    const who = b.consumedBy.length
      ? b.consumedBy.map((c) => `${c.action} 耗 ${c.resource}=${c.amount}`).join("; ")
      : "（初始预算即不足）";
    console.log(
      `  ✗ ${b.goal} (损失收益 ${b.lostProfit}) 看似只差一步 [${b.closestAction}]，` +
        `但关键资源不足: ${crit}`,
    );
    console.log(`    关键资源去向: ${who}`);
  }
}
if (deadEnds.length === 0) console.log("未发现死端。");
