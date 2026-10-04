import assert from "node:assert";
import { Planner, greedyPlan, Problem } from "../src";

const demo: Problem = {
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

// 1. 最优计划达成全部目标：收益 26，成本 10。
{
  const r = new Planner(demo).plan();
  assert.strictEqual(r.profit, 26);
  assert.strictEqual(r.cost, 10);
  assert.deepStrictEqual(
    r.steps.map((s) => s.action).sort(),
    ["careful-patch", "deploy-alpha", "deploy-beta"],
  );
  assert.deepStrictEqual(r.finalBalance, { fuel: 0, parts: 1 });
}

// 2. 每步余额单调不增且永不为负（预算约束）。
{
  const r = new Planner(demo).plan();
  let prev: Record<string, number> = { fuel: 8, parts: 4 };
  for (const s of r.steps) {
    for (const k of Object.keys(prev)) {
      assert.ok(s.balance[k] >= 0, `${k} 余额为负`);
      assert.ok(s.balance[k] <= prev[k], `${k} 不可再生却被补充`);
    }
    prev = s.balance;
  }
}

// 3. 贪心基线陷入死端：收益仅 16，G2 因燃料不足被卡。
{
  const g = greedyPlan(demo);
  assert.strictEqual(g.profit, 16);
  assert.deepStrictEqual(g.finalBalance, { fuel: 1, parts: 4 });
}

// 4. 死端识别：存在 blocked G2、关键资源为 fuel 的死端。
{
  const deadEnds = new Planner(demo).analyzeDeadEnds();
  assert.ok(deadEnds.length > 0, "应检测到死端");
  const trap = deadEnds.find((d) =>
    d.blockedGoals.some(
      (b) =>
        b.goal === "G2" &&
        b.criticalResources.some((c) => c.resource === "fuel"),
    ),
  );
  assert.ok(trap, "应识别 G2 因 fuel 不足的死端");
  const blocked = trap!.blockedGoals.find((b) => b.goal === "G2")!;
  assert.strictEqual(blocked.closestAction, "deploy-beta");
  assert.ok(blocked.consumedBy.length > 0, "应追溯关键资源去向");
}

// 5. 次目标：收益相同时选成本更低的计划。
{
  const tie: Problem = {
    resources: [{ name: "energy", capacity: 10 }],
    goals: [{ name: "G", profit: 5 }],
    actions: [
      { name: "expensive", cost: 9, consumes: { energy: 1 }, achieves: ["G"] },
      { name: "cheap", cost: 2, consumes: { energy: 4 }, achieves: ["G"] },
    ],
  };
  const r = new Planner(tie).plan();
  assert.strictEqual(r.profit, 5);
  assert.strictEqual(r.cost, 2);
  assert.deepStrictEqual(r.steps.map((s) => s.action), ["cheap"]);
}

// 6. 预算不足时跳过目标而非透支。
{
  const poor: Problem = {
    resources: [{ name: "energy", capacity: 2 }],
    goals: [
      { name: "big", profit: 100 },
      { name: "small", profit: 3 },
    ],
    actions: [
      { name: "heavy", cost: 1, consumes: { energy: 5 }, achieves: ["big"] },
      { name: "light", cost: 1, consumes: { energy: 2 }, achieves: ["small"] },
    ],
  };
  const r = new Planner(poor).plan();
  assert.strictEqual(r.profit, 3);
  assert.deepStrictEqual(r.finalBalance, { energy: 0 });
}

// 7. 无解问题：空计划而非报错。
{
  const empty: Problem = {
    resources: [{ name: "energy", capacity: 0 }],
    goals: [{ name: "G", profit: 5 }],
    actions: [{ name: "act", cost: 1, consumes: { energy: 1 }, achieves: ["G"] }],
  };
  const r = new Planner(empty).plan();
  assert.strictEqual(r.profit, 0);
  assert.strictEqual(r.steps.length, 0);
}

// 8. 自环动作（零消耗零收益）不会导致死循环。
{
  const loop: Problem = {
    resources: [{ name: "energy", capacity: 3 }],
    goals: [{ name: "G", profit: 7 }],
    actions: [
      { name: "noop", cost: 0, consumes: {}, achieves: [] },
      { name: "doit", cost: 1, consumes: { energy: 1 }, achieves: ["G"] },
    ],
  };
  const r = new Planner(loop).plan();
  assert.strictEqual(r.profit, 7);
}

// 9. 非法输入被拒绝。
{
  assert.throws(
    () =>
      new Planner({
        resources: [{ name: "x", capacity: 1 }],
        goals: [],
        actions: [{ name: "a", cost: 1, consumes: { ghost: 1 }, achieves: [] }],
      }),
    /未知资源/,
  );
}

console.log("全部 9 项测试通过 ✔");
