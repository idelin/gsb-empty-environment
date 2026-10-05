import { test } from "node:test";
import assert from "node:assert/strict";
import { Planner, REQUEST } from "../src/planner.ts";
import type { PlanEdge } from "../src/planner.ts";
import { makePlugin, makeRegistry } from "./helpers.ts";

test("计划器：解析前置插件与可选依赖，拓扑顺序正确", () => {
  const registry = makeRegistry(
    makePlugin("core"),
    makePlugin("util", { version: "1.2.0", requires: { core: "^1.0.0" } }),
    makePlugin("ui", { optional: { util: "^1.0.0" } }),
    makePlugin("extra", { version: "2.0.0" }),
  );
  const planner = new Planner(registry);
  const plan = planner.plan([{ name: "ui" }, { name: "extra" }]);

  assert.deepEqual([...plan.selected.keys()].sort(), ["core", "extra", "ui", "util"]);
  assert.equal(plan.rejected.length, 0);
  assert.ok(plan.order.indexOf("core") < plan.order.indexOf("util"), "core 必须先于 util");
  assert.ok(plan.order.indexOf("util") < plan.order.indexOf("ui"), "可选依赖在场时也要先初始化");
});

test("计划器：可选依赖缺失时跳过，宿主插件仍然可选中", () => {
  const registry = makeRegistry(makePlugin("ui", { optional: { ghost: "^1.0.0" } }));
  const plan = new Planner(registry).plan([{ name: "ui" }]);

  assert.deepEqual([...plan.selected.keys()], ["ui"]);
  assert.equal(plan.rejected.length, 0);
  assert.equal(plan.optionalSkipped.length, 1);
  assert.equal(plan.optionalSkipped[0].name, "ghost");
});

test("计划器：多版本注册表选择满足约束的最高版本", () => {
  const registry = makeRegistry(
    makePlugin("lib", { version: "1.0.0" }),
    makePlugin("lib", { version: "1.5.0" }),
    makePlugin("lib", { version: "2.0.0" }),
    makePlugin("consumer", { requires: { lib: "^1.2.0" } }),
  );
  const plan = new Planner(registry).plan([{ name: "consumer" }]);

  assert.equal(plan.selected.get("lib")?.manifest.version, "1.5.0");
});

test("计划器：冲突范围导致后请求的插件被拒绝，并给出最小冲突解释", () => {
  const registry = makeRegistry(
    makePlugin("feature-a", { requires: { "lib-c": "^2.0.0" } }),
    makePlugin("tool-b", { conflicts: { "lib-c": ">=2.0.0" } }),
    makePlugin("lib-c", { version: "2.1.0" }),
    makePlugin("solo"),
  );
  const plan = new Planner(registry).plan([{ name: "feature-a" }, { name: "tool-b" }, { name: "solo" }]);

  assert.deepEqual([...plan.selected.keys()].sort(), ["feature-a", "lib-c", "solo"]);
  assert.equal(plan.rejected.length, 1);
  assert.equal(plan.rejected[0].request.name, "tool-b");

  const explanation = plan.rejected[0].explanation;
  assert.equal(explanation.kind, "conflict");
  const keys = explanation.edges.map((edge: PlanEdge) => `${edge.from}->${edge.to}:${edge.kind}`);
  assert.deepEqual(keys.sort(), [
    `${REQUEST}->feature-a:require`,
    `${REQUEST}->tool-b:require`,
    "feature-a->lib-c:require",
    "tool-b->lib-c:conflict",
  ]);
});

test("计划器：最小解释剔除与冲突无关的冗余边", () => {
  const registry = makeRegistry(
    makePlugin("d", { requires: { e: "*", f: "*" } }),
    makePlugin("e", { requires: { f: "*" } }),
    makePlugin("f"),
    makePlugin("g", { conflicts: { f: "*" } }),
  );
  const plan = new Planner(registry).plan([{ name: "d" }, { name: "g" }]);

  assert.equal(plan.rejected.length, 1);
  const explanation = plan.rejected[0].explanation;
  assert.equal(explanation.kind, "conflict");
  const involved = new Set(explanation.edges.flatMap((edge: PlanEdge) => [edge.from, edge.to]));
  assert.ok(!involved.has("e"), "解释不应包含无关插件 e");
  assert.equal(explanation.edges.filter((edge: PlanEdge) => edge.kind === "conflict").length, 1);
});

test("计划器：缺少前置插件时拒绝该请求，保留可独立工作的插件", () => {
  const registry = makeRegistry(
    makePlugin("needy", { requires: { ghost: "^1.0.0" } }),
    makePlugin("independent"),
  );
  const plan = new Planner(registry).plan([{ name: "needy" }, { name: "independent" }]);

  assert.deepEqual([...plan.selected.keys()], ["independent"]);
  assert.equal(plan.rejected.length, 1);
  assert.equal(plan.rejected[0].explanation.kind, "missing-dependency");
  assert.match(plan.rejected[0].explanation.summary, /ghost/);
});

test("计划器：已固定版本与新约束冲突时报告 unsatisfiable-version", () => {
  const registry = makeRegistry(
    makePlugin("lib", { version: "1.5.0" }),
    makePlugin("lib", { version: "2.0.0" }),
    makePlugin("a", { requires: { lib: "^1.0.0" } }),
    makePlugin("b", { requires: { lib: "^2.0.0" } }),
  );
  const plan = new Planner(registry).plan([{ name: "a" }, { name: "b" }]);

  assert.equal(plan.selected.get("lib")?.manifest.version, "1.5.0");
  assert.equal(plan.rejected.length, 1);
  assert.equal(plan.rejected[0].request.name, "b");
  assert.equal(plan.rejected[0].explanation.kind, "unsatisfiable-version");
});

test("计划器：可选依赖引入冲突时跳过可选依赖而不是拖垮宿主", () => {
  const registry = makeRegistry(
    makePlugin("host", { optional: { addon: "*" } }),
    makePlugin("addon", { conflicts: { base: "*" } }),
    makePlugin("base"),
    makePlugin("root", { requires: { base: "*", host: "*" } }),
  );
  const plan = new Planner(registry).plan([{ name: "root" }]);

  assert.equal(plan.rejected.length, 0);
  assert.ok(plan.selected.has("host"));
  assert.ok(!plan.selected.has("addon"), "addon 与 base 冲突，应被跳过");
  assert.equal(plan.optionalSkipped.length, 1);
  assert.equal(plan.optionalSkipped[0].name, "addon");
});
