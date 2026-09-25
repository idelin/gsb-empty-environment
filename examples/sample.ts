import { Pipeline, filterRel, joinRel, aggregateRel, splitRel, field, PathError } from "../src/index.ts";

const show = (label: string, v: unknown) =>
  console.log(`${label}\n${JSON.stringify(v, null, 2)}\n`);

// 1) 两个来源数据集（JSON 兼容树）
const p = new Pipeline();
const orders = p.source("orders", [
  { id: 1, region: "cn", amount: 10 },
  { id: 2, region: "cn", amount: 20 },
  { id: 3, region: "us", amount: 5 },
  { id: 4, region: "cn", amount: -99 }, // 将被过滤
]);
const regions = p.source("regions", [
  { region: "cn", label: "China" },
  { region: "us", label: "USA" },
]);

// 2) 筛选 -> 连接 -> 聚合（血缘随值自动组合）
const kept = p.run("filter-positive", filterRel(orders, (r) => (field(r, "amount", []).value as number) > 0));
const joined = p.run("join-region", joinRel(kept, regions, { left: "region", right: "region" }));
const agg = p.run(
  "agg-by-region",
  aggregateRel(joined, {
    groupBy: ["region"],
    measures: { total: { op: "sum", field: "amount" }, n: { op: "count" } },
  }),
);

show("聚合结果（值）", agg.rows.map((r) => Object.fromEntries(Object.entries(r).map(([k, t]) => [k, t.value]))));

// 3) 反查：$[0].total 的全部贡献来源（追到全部贡献行，被过滤的 id=4 不在其中）
show('explain("$[0].total")', p.explain("$[0].total"));

// 4) 按版本查询：版本 4 = join-region 阶段
show("lineage(version=4, join-region)", p.lineage(4));

// 5) 拆分：按区域分组后单独提交，组与行均保序
const groups = splitRel(orders, (r) => field(r, "region", []).value as string);
p.run("split-cn", groups.get("cn")!);
show("lineage(version=6, split-cn)", p.lineage(6));

// 6) 错误定位：对非数值字段求和，错误定位到来源具体路径
try {
  const bad = p.source("bad", [{ amount: 1 }, { amount: "oops" }]);
  aggregateRel(bad, { measures: { t: { op: "sum", field: "amount" } } });
} catch (err) {
  if (err instanceof PathError) {
    console.log(`PathError: ${err.message}`);
    console.log(`  source=${err.source} path=${JSON.stringify(err.path)}\n`);
  }
}
