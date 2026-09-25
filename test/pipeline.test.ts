import { test } from "node:test";
import assert from "node:assert/strict";
import { Pipeline } from "../src/pipeline.ts";
import { filterRel, joinRel, aggregateRel, splitRel } from "../src/ops.ts";
import { field } from "../src/relation.ts";

function build() {
  const p = new Pipeline();
  const orders = p.source("orders", [
    { id: 1, region: "cn", amount: 10 },
    { id: 2, region: "cn", amount: 20 },
    { id: 3, region: "us", amount: 5 },
    { id: 4, region: "cn", amount: -99 },
  ]);
  const regions = p.source("regions", [
    { region: "cn", label: "China" },
    { region: "us", label: "USA" },
  ]);
  const kept = p.run("filter-positive", filterRel(orders, (r) => (field(r, "amount", []).value as number) > 0));
  const joined = p.run("join-region", joinRel(kept, regions, { left: "region", right: "region" }));
  const agg = p.run("agg-by-region", aggregateRel(joined, {
    groupBy: ["region"],
    measures: { total: { op: "sum", field: "amount" }, n: { op: "count" } },
  }));
  return { p, orders, regions, kept, joined, agg };
}

test("端到端：组合 filter/join/aggregate，聚合输出追到全部贡献行", () => {
  const { p, agg } = build();
  const cn = agg.rows.find((r) => field(r, "region", []).value === "cn")!;
  assert.equal(field(cn, "total", []).value, 30);

  const cnIdx = agg.rows.indexOf(cn);
  const ev = p.explain(`$[${cnIdx}].total`);
  // 穿过 join 与 filter，定位到 orders 原始路径；被过滤的 id=4 不出现
  assert.deepEqual(ev, [
    { source: "orders", path: [0, "amount"] },
    { source: "orders", path: [1, "amount"] },
  ]);
});

test("被过滤的行不出现在血缘", () => {
  const { p } = build();
  // 过滤阶段（版本 3）及其后的所有派生血缘中，都不含被过滤的 orders 第 3 行
  const derived = [3, 4, 5].flatMap((v) => p.lineage(v)).flatMap((r) => r.evidence);
  assert.ok(derived.length > 0);
  assert.ok(!derived.some((e) => e.source === "orders" && e.path[0] === 3));
});

test("按版本查询：每个阶段一个版本", () => {
  const { p } = build();
  assert.equal(p.store.latestVersion, 5);
  const v4 = p.lineage(4); // join-region
  assert.ok(v4.length > 0);
  assert.ok(v4.every((r) => r.stage === "join-region"));
  // join 后 region 字段证据来自两个来源
  const regionRec = v4.find((r) => r.output === "$[0].region")!;
  assert.deepEqual(regionRec.evidence, [
    { source: "orders", path: [0, "region"] },
    { source: "regions", path: [0, "region"] },
  ]);
  // asOf(2) 只含两个 source 阶段的输出
  assert.ok(p.asOf(2).every((r) => r.stage.startsWith("source:")));
});

test("split 结果也可提交并独立查询", () => {
  const p = new Pipeline();
  const src = p.source("s", [
    { k: "a", v: 1 },
    { k: "b", v: 2 },
  ]);
  const groups = splitRel(src, (r) => field(r, "k", []).value as string);
  p.run("split-a", groups.get("a")!);
  const recs = p.lineage(2);
  assert.deepEqual(recs, [
    { version: 2, stage: "split-a", output: "$[0].k", evidence: [{ source: "s", path: [0, "k"] }] },
    { version: 2, stage: "split-a", output: "$[0].v", evidence: [{ source: "s", path: [0, "v"] }] },
  ]);
});
