import { test } from "node:test";
import assert from "node:assert/strict";
import { DynamicSCC } from "../src/incremental-scc.ts";

function componentIds(g: DynamicSCC): string[] {
  return g.getComponents().map((c) => c.id);
}

test("三角形环 A->B->C->A 合并为一个分量, 标识为最小节点 ID", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  assert.deepEqual(componentIds(g), ["A", "B", "C"]);

  const diff = g.addEdge("C", "A");
  assert.equal(diff.merged.length, 1);
  assert.equal(diff.merged[0].component.id, "A");
  assert.deepEqual(diff.merged[0].component.members, ["A", "B", "C"]);
  assert.deepEqual(
    diff.merged[0].absorbed.map((c) => c.id),
    ["A", "B", "C"],
  );
  assert.equal(diff.split.length, 0);

  assert.equal(g.componentOf("A"), "A");
  assert.equal(g.componentOf("B"), "A");
  assert.equal(g.componentOf("C"), "A");
  assert.deepEqual(componentIds(g), ["A"]);
});

test("加入独立节点 D 及边 C->D 不产生合并, D 保持单节点分量", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  g.addEdge("C", "A");

  g.addVertex("D");
  const diff = g.addEdge("C", "D");
  assert.equal(diff.merged.length, 0);
  assert.equal(diff.split.length, 0);

  assert.equal(g.componentOf("D"), "D");
  assert.deepEqual(componentIds(g), ["A", "D"]);
});

test("删除边 B->C 后原分量精准分裂为 A/B/C 单节点分量, D 归属不受影响", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  g.addEdge("C", "A");
  g.addVertex("D");
  g.addEdge("C", "D");

  const diff = g.removeEdge("B", "C");
  assert.equal(diff.merged.length, 0);
  assert.equal(diff.split.length, 1);
  assert.deepEqual(diff.split[0].from, { id: "A", members: ["A", "B", "C"] });
  assert.deepEqual(diff.split[0].components, [
    { id: "A", members: ["A"] },
    { id: "B", members: ["B"] },
    { id: "C", members: ["C"] },
  ]);

  assert.equal(g.componentOf("A"), "A");
  assert.equal(g.componentOf("B"), "B");
  assert.equal(g.componentOf("C"), "C");
  assert.equal(g.componentOf("D"), "D");
  assert.deepEqual(componentIds(g), ["A", "B", "C", "D"]);
});

test("删除不破坏环的边不产生分裂", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  g.addEdge("C", "A");
  g.addEdge("A", "C"); // 冗余边

  const diff = g.removeEdge("A", "C");
  assert.equal(diff.split.length, 0);
  assert.deepEqual(componentIds(g), ["A"]);
});

test("多分量合并: 桥接边形成大环时跨分量合并", () => {
  const g = new DynamicSCC();
  // 环1: A<->B, 环2: C<->D, 桥: B->C
  g.addEdge("A", "B");
  g.addEdge("B", "A");
  g.addEdge("C", "D");
  g.addEdge("D", "C");
  g.addEdge("B", "C");
  assert.deepEqual(componentIds(g), ["A", "C"]);

  // D->A 闭合大环, 两个分量合并
  const diff = g.addEdge("D", "A");
  assert.equal(diff.merged.length, 1);
  assert.equal(diff.merged[0].component.id, "A");
  assert.deepEqual(diff.merged[0].component.members, ["A", "B", "C", "D"]);
  assert.deepEqual(componentIds(g), ["A"]);
});

test("分量标识取成员最小节点 ID 而非字典序最小原分量", () => {
  const g = new DynamicSCC();
  g.addEdge("X", "Y");
  const diff = g.addEdge("Y", "X");
  assert.equal(diff.merged[0].component.id, "X");
  assert.equal(g.componentOf("Y"), "X");
});

test("删除顶点级联删除关联边并触发分裂", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  g.addEdge("C", "A");
  g.addEdge("C", "D");
  g.addEdge("D", "C"); // C<->D 环? C->D, D->C 在环内, 全部一体
  assert.deepEqual(componentIds(g), ["A"]);

  // 删除 C: 关联边逐条级联移除, 分量 {A,B,C,D} 先分裂出 {A},{B},{C,D}, 再分裂 {C},{D}
  const diff = g.removeVertex("C");
  assert.equal(diff.split.length, 2);
  assert.deepEqual(diff.removed, [{ id: "C", members: ["C"] }]);
  assert.equal(g.hasVertex("C"), false);
  assert.equal(g.componentOf("C"), undefined);
  assert.deepEqual(componentIds(g), ["A", "B", "D"]);
});

test("重复边与自环为无操作", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  const d1 = g.addEdge("A", "B");
  assert.equal(d1.merged.length, 0);
  const d2 = g.addEdge("A", "A");
  assert.equal(d2.merged.length, 0);
  const d3 = g.removeEdge("B", "A");
  assert.equal(d3.split.length, 0);
  assert.deepEqual(componentIds(g), ["A", "B"]);
});

test("分裂后分量可再次增量合并, 标识保持稳定", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  g.addEdge("C", "A");
  g.removeEdge("B", "C");
  assert.deepEqual(componentIds(g), ["A", "B", "C"]);

  // 重新加回 B->C, 再次合并为以 A 标识的分量
  const diff = g.addEdge("B", "C");
  assert.equal(diff.merged[0].component.id, "A");
  assert.deepEqual(diff.merged[0].component.members, ["A", "B", "C"]);
  assert.deepEqual(componentIds(g), ["A"]);
});

test("getLastDiff 返回最近一次操作的差量", () => {
  const g = new DynamicSCC();
  g.addEdge("A", "B");
  g.addEdge("B", "A");
  const last = g.getLastDiff();
  assert.equal(last.merged.length, 1);
  assert.equal(last.merged[0].component.id, "A");
});
