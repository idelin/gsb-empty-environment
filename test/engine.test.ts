import { test } from "node:test";
import assert from "node:assert/strict";
import { ReasoningEngine } from "../src/engine.ts";

test("验收: 已知 A 与 A->B，推导出 B", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addRule(["A"], "B");
  assert.equal(e.isKnown("B"), true);
  const exp = e.explain("B")!;
  assert.deepEqual(exp.baseFacts, ["A"]);
  assert.equal(exp.depth, 1);
  assert.equal(exp.proof.fact, "B");
  assert.equal(exp.proof.children[0].fact, "A");
});

test("验收: 有 C 与 C->B 时撤销 A，B 因 C 保留", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addRule(["A"], "B");
  e.addFact("C");
  e.addRule(["C"], "B");
  e.retractFact("A");
  assert.equal(e.isKnown("A"), false);
  assert.equal(e.isKnown("B"), true);
  assert.deepEqual(e.explain("B")!.baseFacts, ["C"]);
});

test("验收: 再撤销 C，B 失去全部支持被清除", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addRule(["A"], "B");
  e.addFact("C");
  e.addRule(["C"], "B");
  e.retractFact("A");
  e.retractFact("C");
  assert.equal(e.isKnown("B"), false);
  assert.equal(e.explain("B"), null);
  assert.deepEqual(e.knownFacts(), []);
});

test("多级链式推导与级联清除", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addRule(["A"], "B");
  e.addRule(["B"], "D");
  assert.equal(e.isKnown("D"), true);
  const exp = e.explain("D")!;
  assert.deepEqual(exp.baseFacts, ["A"]);
  assert.equal(exp.depth, 2);
  e.retractFact("A");
  assert.equal(e.isKnown("B"), false);
  assert.equal(e.isKnown("D"), false);
});

test("多前件规则: 全部前件满足才触发", () => {
  const e = new ReasoningEngine();
  e.addRule(["P", "Q"], "R");
  e.addFact("P");
  assert.equal(e.isKnown("R"), false);
  e.addFact("Q");
  assert.equal(e.isKnown("R"), true);
  assert.deepEqual(e.explain("R")!.baseFacts, ["P", "Q"]);
  e.retractFact("Q");
  assert.equal(e.isKnown("R"), false);
});

test("菱形依赖: 撤销共同祖先后全部派生结论清除", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addRule(["A"], "B");
  e.addRule(["A"], "C");
  e.addRule(["B", "C"], "D");
  assert.equal(e.isKnown("D"), true);
  assert.deepEqual(e.explain("D")!.baseFacts, ["A"]);
  e.retractFact("A");
  assert.deepEqual(e.knownFacts(), []);
});

test("最小解释: 优先选择证据集更小、深度更浅的证明", () => {
  const e = new ReasoningEngine();
  e.addFact("X");
  e.addFact("Y");
  e.addFact("Z");
  // 长链: Z -> E -> B（2 步）
  e.addRule(["Z"], "E");
  e.addRule(["E"], "B");
  // 短链: X -> B（1 步）
  e.addRule(["X"], "B");
  // 双证据: Y & Z -> B（2 个基础事实）
  e.addRule(["Y", "Z"], "B");
  const exp = e.explain("B")!;
  assert.deepEqual(exp.baseFacts, ["X"]);
  assert.equal(exp.depth, 1);
  // 撤销 X 后退回次优解释
  e.retractFact("X");
  const exp2 = e.explain("B")!;
  assert.deepEqual(exp2.baseFacts, ["Z"]);
  assert.equal(exp2.depth, 2);
});

test("撤销后重新断言可恢复推导", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addRule(["A"], "B");
  e.retractFact("A");
  assert.equal(e.isKnown("B"), false);
  e.addFact("A");
  assert.equal(e.isKnown("B"), true);
});

test("规则先于事实添加也能触发（正向链监听）", () => {
  const e = new ReasoningEngine();
  e.addRule(["A"], "B");
  assert.equal(e.isKnown("B"), false);
  e.addFact("A");
  assert.equal(e.isKnown("B"), true);
});

test("重复断言幂等；撤销非基础事实与未知事实安全", () => {
  const e = new ReasoningEngine();
  e.addFact("A");
  e.addFact("A");
  e.addRule(["A"], "B");
  e.retractFact("B"); // B 是派生事实，忽略
  assert.equal(e.isKnown("B"), true);
  e.retractFact("NoSuch"); // 未知事实，忽略
  e.retractFact("A");
  e.retractFact("A"); // 重复撤销幂等
  assert.equal(e.isKnown("B"), false);
});

test("环形规则不会死循环", () => {
  const e = new ReasoningEngine();
  e.addRule(["A"], "B");
  e.addRule(["B"], "A");
  e.addFact("A");
  assert.equal(e.isKnown("B"), true);
  const exp = e.explain("B")!;
  assert.deepEqual(exp.baseFacts, ["A"]);
  e.retractFact("A");
  assert.equal(e.isKnown("B"), false);
});

test("空前件规则等价于无条件结论", () => {
  const e = new ReasoningEngine();
  e.addRule([], "T");
  assert.equal(e.isKnown("T"), true);
  assert.deepEqual(e.explain("T")!.baseFacts, []);
});

test("查询未知事实的解释返回 null", () => {
  const e = new ReasoningEngine();
  assert.equal(e.explain("Ghost"), null);
});
