import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";
import { optimize, type JoinNode, type OptimizeInput } from "../src/optimizer.ts";

test("Rational: 解析与约分", () => {
  assert.equal(Rational.parse("6/8").toString(), "3/4");
  assert.equal(Rational.parse("0.75").toString(), "3/4");
  assert.equal(Rational.parse("1e-3").toString(), "1/1000");
  assert.equal(Rational.parse("-2/4").toString(), "-1/2");
  assert.equal(Rational.parse("12").toString(), "12");
});

test("Rational: 四则运算与比较", () => {
  const a = Rational.parse("1/3");
  const b = Rational.parse("1/6");
  assert.equal(a.add(b).toString(), "1/2");
  assert.equal(a.sub(b).toString(), "1/6");
  assert.equal(a.mul(b).toString(), "1/18");
  assert.equal(a.div(b).toString(), "2");
  assert.ok(a.cmp(b) > 0);
  assert.equal(Rational.parse("2/4").toDecimal(3), "0.5");
  assert.equal(Rational.parse("1/3").toDecimal(4), "0.3333");
});

const chain3: OptimizeInput = {
  tables: [
    { name: "A", cardinality: "100" },
    { name: "B", cardinality: "200" },
    { name: "C", cardinality: "50" },
  ],
  edges: [
    { tables: ["A", "B"], selectivity: "1/10" },
    { tables: ["B", "C"], selectivity: "1/5" },
  ],
};

test("三表链：精确最优成本 22000（允许笛卡尔积）", () => {
  const r = optimize({ ...chain3, allowCrossProducts: true });
  assert.equal(r.optimalCost.toString(), "22000");
  assert.equal(r.plan.kind, "join");
  const root = r.plan as JoinNode;
  assert.equal(root.cardinality.toString(), "20000");
  // 3 表共 7 个非空子集全部可达
  assert.equal(r.stats.reachableSubsets, 7);
  // 估算表包含每个子计划
  const keys = r.estimates.map((e) => e.tables.join("")).sort();
  assert.deepEqual(keys, ["A", "AB", "ABC", "AC", "B", "BC", "C"]);
});

test("禁止笛卡尔积：链上仍可优化，结果一致", () => {
  const r = optimize({ ...chain3, allowCrossProducts: false });
  assert.equal(r.optimalCost.toString(), "22000");
  // 不可达子集 {A,C} 被排除
  assert.equal(r.stats.reachableSubsets, 6);
});

test("禁止笛卡尔积：图不连通时报错", () => {
  assert.throws(
    () =>
      optimize({
        tables: [
          { name: "A", cardinality: "10" },
          { name: "B", cardinality: "10" },
        ],
        edges: [],
        allowCrossProducts: false,
      }),
    /不存在完整连接计划/,
  );
});

test("bushy 计划优于左深：最优为 (A⋈B)⋈(C⋈D)", () => {
  const r = optimize({
    tables: [
      { name: "A", cardinality: "1000" },
      { name: "B", cardinality: "1000" },
      { name: "C", cardinality: "1000" },
      { name: "D", cardinality: "1000" },
    ],
    edges: [
      { tables: ["A", "B"], selectivity: "1/1000" },
      { tables: ["C", "D"], selectivity: "1/1000" },
    ],
    allowCrossProducts: true,
  });
  // 1000 + 1000 + 10^6
  assert.equal(r.optimalCost.toString(), "1002000");
  const root = r.plan as JoinNode;
  assert.equal(root.isCrossProduct, true);
  // 根的两个子节点都是两表连接（bushy，而非左深）
  assert.equal(root.left.kind, "join");
  assert.equal(root.right.kind, "join");
  const leftTables = (root.left as JoinNode).tables;
  const rightTables = (root.right as JoinNode).tables;
  assert.deepEqual([...leftTables].sort(), ["A", "B"]);
  assert.deepEqual([...rightTables].sort(), ["C", "D"]);
});

test("物化代价计入中间结果（根节点默认不计）", () => {
  const r = optimize({
    ...chain3,
    allowCrossProducts: true,
    materialization: { perRow: "1/10" },
  });
  // 最优 (A⋈B)⋈C：中间结果 2000 + 物化 200 + 根 20000 = 22200
  assert.equal(r.optimalCost.toString(), "22200");
  const root = r.plan as JoinNode;
  assert.equal(root.materializationCost.toString(), "0");
  const inner = root.left.kind === "join" ? root.left : (root.right as JoinNode);
  assert.equal((inner as JoinNode).materializationCost.toString(), "200");
});

test("物化代价：includeRoot 与固定代价", () => {
  const r = optimize({
    tables: [
      { name: "A", cardinality: "10" },
      { name: "B", cardinality: "20" },
    ],
    edges: [{ tables: ["A", "B"], selectivity: "1/2" }],
    materialization: { perRow: "1/10", fixed: "5", includeRoot: true },
  });
  // 结果基数 100，物化 100/10+5=15，总成本 115
  assert.equal(r.optimalCost.toString(), "115");
});

test("12 张表链式查询：性能与连通子集计数", () => {
  const n = 12;
  const tables = Array.from({ length: n }, (_, i) => ({
    name: `T${i}`,
    cardinality: "1000",
  }));
  const edges = Array.from({ length: n - 1 }, (_, i) => ({
    tables: [`T${i}`, `T${i + 1}`] as [string, string],
    selectivity: "1/100",
  }));
  const start = Date.now();
  const r = optimize({ tables, edges, allowCrossProducts: false });
  const elapsed = Date.now() - start;
  // 链上连通子集 = 连续区间数 = n(n+1)/2
  assert.equal(r.stats.reachableSubsets, (n * (n + 1)) / 2);
  assert.ok(r.optimalCost.cmp(Rational.ZERO) > 0);
  assert.ok(elapsed < 5000, `耗时过长: ${elapsed}ms`);
});

test("超过 12 张表报错", () => {
  const tables = Array.from({ length: 13 }, (_, i) => ({
    name: `T${i}`,
    cardinality: "10",
  }));
  assert.throws(() => optimize({ tables }), /最多支持 12 张表/);
});
