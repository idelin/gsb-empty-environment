import { test } from "node:test";
import assert from "node:assert/strict";
import { Reasoner } from "../src/horn.ts";

test("验收:已知 A 与 A->B,推导出 B", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.assert("A");
  assert.ok(r.knows("B"));
});

test("验收:撤销 A 后,B 因独立证据 C 保留", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.addRule({ id: "r2", premises: ["C"], conclusion: "B" });
  r.assert("A");
  r.assert("C");
  assert.ok(r.knows("B"));

  r.retract("A");
  assert.ok(!r.knows("A"));
  assert.ok(r.knows("B"), "B 应因 C->B 继续成立");
  assert.deepEqual(
    r.justificationsOf("B").map((j) => j.ruleId),
    ["r2"],
  );
});

test("验收:再撤销 C,B 失去全部支持被安全清除", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.addRule({ id: "r2", premises: ["C"], conclusion: "B" });
  r.assert("A");
  r.assert("C");
  r.retract("A");
  r.retract("C");
  assert.ok(!r.knows("B"), "B 失去全部支持应被清除");
  assert.equal(r.explain("B"), null);
});

test("级联清除:多级推导链整体回收", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.addRule({ id: "r2", premises: ["B"], conclusion: "D" });
  r.addRule({ id: "r3", premises: ["D", "E"], conclusion: "F" });
  r.assert("A");
  r.assert("E");
  assert.ok(r.knows("F"));

  r.retract("A");
  assert.ok(!r.knows("B"));
  assert.ok(!r.knows("D"));
  assert.ok(!r.knows("F"), "F 的间接支持消失,应被级联清除");
  assert.ok(r.knows("E"), "无关基础事实不受影响");
});

test("中间节点的独立支持可阻断级联", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.addRule({ id: "r2", premises: ["G"], conclusion: "B" });
  r.addRule({ id: "r3", premises: ["B"], conclusion: "D" });
  r.assert("A");
  r.assert("G");
  r.retract("A");
  assert.ok(r.knows("B"));
  assert.ok(r.knows("D"), "B 仍成立,下游 D 应保留");
});

test("最小解释:多前提合取与证据链拓扑序", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.addRule({ id: "r2", premises: ["B", "C"], conclusion: "D" });
  r.assert("A");
  r.assert("C");

  const ex = r.explain("D");
  assert.ok(ex);
  assert.equal(ex.depth, 2);
  assert.deepEqual(ex.baseFacts, ["A", "C"]);
  assert.deepEqual(
    ex.steps.map((s) => s.conclusion),
    ["B", "D"],
  );
  assert.deepEqual(ex.steps[1].premises, ["B", "C"]);
});

test("最小解释:多条证明路径时选择最短证据链", () => {
  const r = new Reasoner();
  // 长路径: A -> X -> Y -> B
  r.addRule({ id: "long1", premises: ["A"], conclusion: "X" });
  r.addRule({ id: "long2", premises: ["X"], conclusion: "Y" });
  r.addRule({ id: "long3", premises: ["Y"], conclusion: "B" });
  // 短路径: C -> B
  r.addRule({ id: "short", premises: ["C"], conclusion: "B" });
  r.assert("A");
  r.assert("C");

  const ex = r.explain("B");
  assert.ok(ex);
  assert.equal(ex.depth, 1);
  assert.deepEqual(ex.baseFacts, ["C"]);
  assert.deepEqual(
    ex.steps.map((s) => s.ruleId),
    ["short"],
  );
});

test("基础事实的解释是其自身", () => {
  const r = new Reasoner();
  r.assert("A");
  const ex = r.explain("A");
  assert.ok(ex);
  assert.equal(ex.depth, 0);
  assert.deepEqual(ex.baseFacts, ["A"]);
  assert.deepEqual(ex.steps, []);
});

test("撤销后重新断言可恢复推导", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.assert("A");
  r.retract("A");
  assert.ok(!r.knows("B"));
  r.assert("A");
  assert.ok(r.knows("B"));
});

test("规则后注册:对已有事实补触发", () => {
  const r = new Reasoner();
  r.assert("A");
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  assert.ok(r.knows("B"));
});

test("撤销非基础事实与未知事实为空操作", () => {
  const r = new Reasoner();
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  r.assert("A");
  r.retract("B");       // B 是派生事实,不能直接撤销
  r.retract("ZZZ");     // 未知事实
  assert.ok(r.knows("A"));
  assert.ok(r.knows("B"));
});

test("重复断言与重复规则", () => {
  const r = new Reasoner();
  r.assert("A");
  r.assert("A");
  assert.ok(r.knows("A"));
  r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
  assert.throws(() => r.addRule({ id: "r1", premises: [], conclusion: "Z" }));
});
