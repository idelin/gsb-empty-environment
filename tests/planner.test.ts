import test from "node:test";
import assert from "node:assert/strict";
import { buildState, resolveRef } from "../src/tree.ts";
import {
  assertIdentityPreserved,
  findViolations,
  planMigration,
} from "../src/planner.ts";
import { scenarioNodes, targetGrouping } from "../src/scenario.ts";
import type { NodeSpec } from "../src/types.ts";

test("演示场景：3 次移动满足目标分组，次目标权限例外最少", () => {
  const initial = buildState(scenarioNodes);
  assert.equal(initial.parent.size, 15);
  const result = planMigration(initial, targetGrouping);
  assert.ok(result);
  assert.equal(result.moves.length, 3);
  assert.deepEqual(
    result.moves.map((m) => `${m.nodeId}->${m.newParentId}`).sort(),
    ["brand->shared", "ml->eng", "runbooks->ops"],
  );
  assert.equal(findViolations(result.finalState, targetGrouping).length, 0);
  assert.equal(result.exceptionNodeCount, 5);
  assert.equal(result.exceptionEntryCount, 17);
  assertIdentityPreserved(initial, result.finalState);
});

test("引用解析：迁移前后每条引用均有解析结果", () => {
  const initial = buildState(scenarioNodes);
  const result = planMigration(initial, targetGrouping);
  assert.ok(result);
  const byKey = new Map(result.refResolutions.map((r) => [`${r.nodeId}|${r.path}`, r]));
  assert.equal(byKey.get("ml|../scripts")?.before, "scripts");
  assert.equal(byKey.get("ml|../scripts")?.after, null);
  assert.equal(byKey.get("ml|../scripts")?.status, "BROKEN");
  assert.equal(byKey.get("api|../db")?.status, "UNCHANGED");
  assert.equal(byKey.get("api|../../../shared/docs")?.before, "docs");
  assert.equal(byKey.get("assets|../../frontend")?.status, "BROKEN");
  assert.equal(byKey.get("datasets|..")?.after, "ml");
});

test("已满足目标分组时无需移动", () => {
  const initial = buildState(scenarioNodes);
  const result = planMigration(initial, { mustBeUnder: { api: "eng", docs: "shared" } });
  assert.ok(result);
  assert.equal(result.moves.length, 0);
  assert.equal(result.exceptionNodeCount, 0);
});

test("固定节点不可移动：唯一修复路径被固定时返回不可解", () => {
  const specs: NodeSpec[] = [
    { id: "root", name: "root", parent: null, pinned: true },
    { id: "a", name: "a", parent: "root", pinned: true },
    { id: "b", name: "b", parent: "root", pinned: true },
    { id: "x", name: "x", parent: "a", pinned: true },
  ];
  const initial = buildState(specs);
  const result = planMigration(initial, { mustBeUnder: { x: "b" } });
  assert.equal(result, null);
});

test("移动祖先子树一次可修复多个后代违规", () => {
  const specs: NodeSpec[] = [
    { id: "root", name: "root", parent: null, pinned: true },
    { id: "g1", name: "g1", parent: "root", pinned: true },
    { id: "g2", name: "g2", parent: "root", pinned: true },
    { id: "sub", name: "sub", parent: "g1" },
    { id: "c1", name: "c1", parent: "sub" },
    { id: "c2", name: "c2", parent: "sub" },
  ];
  const initial = buildState(specs);
  const result = planMigration(initial, {
    mustBeUnder: { sub: "g2", c1: "g2", c2: "g2" },
  });
  assert.ok(result);
  assert.equal(result.moves.length, 1);
  assert.deepEqual(result.moves[0], { nodeId: "sub", newParentId: "g2" });
});

test("重复节点 id 被拒绝（禁止复制虚增身份）", () => {
  assert.throws(() =>
    buildState([
      { id: "root", name: "root", parent: null },
      { id: "a", name: "a", parent: "root" },
      { id: "a", name: "a2", parent: "root" },
    ]),
  );
});

test("相对引用解析：. 与 .. 与越界", () => {
  const initial = buildState(scenarioNodes);
  assert.equal(resolveRef(initial, "api", "."), "api");
  assert.equal(resolveRef(initial, "api", "../.."), "eng");
  assert.equal(resolveRef(initial, "root", ".."), null);
  assert.equal(resolveRef(initial, "frontend", "./nonexistent"), null);
});
