import { describe, it } from "node:test";
import assert from "node:assert/strict";
import type { RuleSet } from "../src/types.ts";
import { analyzeRuleset, searchIdempotency } from "../src/dependency.ts";

const rs = (rules: RuleSet["rules"]): RuleSet => ({ name: "t", rules });

describe("规则组合依赖分析", () => {
  it("显式顺序与读写路径依赖", () => {
    const a = analyzeRuleset(rs([
      { id: "w", type: "whitespace", path: "/a/*/n", mode: "trim" },
      { id: "e", type: "enum", path: "/a/*/n", map: { x: "z" } },
    ]));
    assert.equal(a.ok, true);
    assert.deepEqual(a.order, ["w", "e"]);
    assert.deepEqual(a.dependencies.map((d) => [d.ruleId, d.reads, d.writes]), [
      ["w", ["/a/*/n"], ["/a/*/n"]],
      ["e", ["/a/*/n"], ["/a/*/n"]],
    ]);
    assert.deepEqual(a.overlapPairs, [["w", "e"]]);
  });

  it("提前发现别名冲突（同字段别名映射到不同规范值）", () => {
    const a = analyzeRuleset(rs([
      { id: "e1", type: "enum", path: "/s", map: { A: "x" } },
      { id: "e2", type: "enum", path: "/s", map: { A: "y" } },
    ]));
    assert.equal(a.ok, false);
    assert.equal(a.aliasConflicts.length, 1);
    assert.deepEqual(a.aliasConflicts[0].canonical, ["x", "y"]);
  });

  it("不同字段上的同名别名不构成冲突", () => {
    const a = analyzeRuleset(rs([
      { id: "e1", type: "enum", path: "/a", map: { A: "x" } },
      { id: "e2", type: "enum", path: "/b", map: { A: "y" } },
    ]));
    assert.equal(a.aliasConflicts.length, 0);
    assert.deepEqual(a.overlapPairs, []);
  });

  it("检测相互触发环（含自环与双规则环）", () => {
    const self = analyzeRuleset(rs([
      { id: "e", type: "enum", path: "/s", map: { A: "B", B: "A" } },
    ]));
    assert.equal(self.ok, false);
    assert.deepEqual(self.cycles[0].rules, ["e", "e"]);

    const two = analyzeRuleset(rs([
      { id: "e1", type: "enum", path: "/s", map: { A: "B" } },
      { id: "e2", type: "enum", path: "/s", map: { B: "A" } },
    ]));
    assert.equal(two.cycles.length >= 1, true);
    const joined = two.cycles.map((c) => c.rules.join(","));
    assert.ok(joined.some((j) => j.includes("e1") && j.includes("e2")));
  });

  it("前向触发边合法：A 规范为 x，后续规则消费 x", () => {
    const a = analyzeRuleset(rs([
      { id: "e1", type: "enum", path: "/s", map: { A: "x" } },
      { id: "e2", type: "enum", path: "/s", map: { x: "z" } },
    ]));
    assert.equal(a.ok, true);
    assert.deepEqual(a.triggerEdges.map((e) => [e.from, e.to]), [["e1", "e2"]]);
    assert.equal(a.orderViolations.length, 0);
  });

  it("后写触发先读 => 顺序违例（单遍管道会漏改）", () => {
    const a = analyzeRuleset(rs([
      { id: "e2", type: "enum", path: "/s", map: { x: "z" } },
      { id: "e1", type: "enum", path: "/s", map: { A: "x" } },
    ]));
    assert.equal(a.ok, false);
    assert.equal(a.orderViolations.length, 1);
    assert.equal(a.orderViolations[0].edge.from, "e1");
    assert.equal(a.orderViolations[0].edge.to, "e2");
  });

  it("空白输出触发后续枚举（顺序正确则合法）", () => {
    const a = analyzeRuleset(rs([
      { id: "w", type: "whitespace", path: "/s", mode: "trim" },
      { id: "e", type: "enum", path: "/s", map: { x: "z" } },
    ]));
    assert.equal(a.ok, true);
    assert.deepEqual(a.triggerEdges.map((e) => [e.from, e.to, e.witness]), [["w", "e", "x"]]);
  });

  it("重复 id 与形状非法被拒", () => {
    const a = analyzeRuleset(rs([
      { id: "dup", type: "whitespace", path: "/s", mode: "trim" },
      { id: "dup", type: "whitespace", path: "/t", mode: "trim" },
    ]));
    assert.ok(a.duplicateIds.includes("dup"));
    assert.equal(a.ok, false);

    const bad = analyzeRuleset({ name: "b", rules: [{ id: "x", type: "date", path: "s", formats: [] }] } as unknown as RuleSet);
    assert.equal(bad.ok, false);
  });
});

describe("幂等反例搜索", () => {
  it("良好组合在声明域上被证明（proved，穷尽）", () => {
    const r = searchIdempotency(rs([
      { id: "w", type: "whitespace", path: "/s", mode: "trim" },
      { id: "d", type: "date", path: "/s", formats: ["YYYY-MM-DD"], outputFormat: "YYYY-MM-DD" },
      { id: "e", type: "enum", path: "/s", map: { M: "male", male: "male" } },
    ]));
    assert.equal(r.status, "proved");
    assert.equal(r.evaluated, r.domainSize);
    assert.equal(r.counterexamples.length, 0);
  });

  it("别名来回映射给出反例（refuted）", () => {
    const r = searchIdempotency(rs([
      { id: "e", type: "enum", path: "/s", map: { A: "B", B: "A" } },
    ]));
    assert.equal(r.status, "refuted");
    const c = r.counterexamples.find((x) => x.input === "A");
    assert.deepEqual(c ? [c.once, c.twice] : null, ["B", "A"]);
  });

  it("两规则环：组合搜索可能恰好不动点，但静态分析独立判环拒绝", () => {
    const combo = rs([
      { id: "e1", type: "enum", path: "/s", map: { A: "B" } },
      { id: "e2", type: "enum", path: "/s", map: { B: "A" } },
    ]);
    // 单遍组合 A->B->A 回到 A，f^2=f 在声明域上成立
    assert.equal(searchIdempotency(combo).status, "proved");
    // 但触发环独立成立：相互触发的组合仍被静态分析拒绝
    const a = analyzeRuleset(combo);
    assert.equal(a.ok, false);
    assert.ok(a.cycles.length >= 1);
  });

  it("跨遍回踢环给出真正的幂等反例（第二遍仍改变）", () => {
    // 单遍: A -e1-> B -e2-> C -e3-> D（f(A)=D）；
    // 第二遍: D -e1-> A 后无后续命中（f(D)=A）；A != D
    const r = searchIdempotency(rs([
      { id: "e1", type: "enum", path: "/s", map: { A: "B", D: "A" } },
      { id: "e2", type: "enum", path: "/s", map: { B: "C" } },
      { id: "e3", type: "enum", path: "/s", map: { C: "D" } },
    ]));
    assert.equal(r.status, "refuted");
    const c = r.counterexamples.find((x) => x.input === "A" || x.input === "D");
    assert.ok(c && c.once !== c.twice, JSON.stringify(r.counterexamples));
  });

  it("预算 0 => undecided，绝不标 proved", () => {
    const r = searchIdempotency(rs([
      { id: "w", type: "whitespace", path: "/s", mode: "trim" },
    ]), { budget: 0 });
    assert.equal(r.status, "undecided");
    assert.equal(r.evaluated, 0);
    assert.match(r.note, /预算/);
  });

  it("不同字段的规则不会互相触发（代表分组隔离）", () => {
    const r = searchIdempotency(rs([
      { id: "a", type: "enum", path: "/a", map: { A: "B" } },
      { id: "b", type: "enum", path: "/b", map: { B: "A" } },
    ]));
    // 单条自稳：两个字段互不影响，故无环且幂等
    assert.equal(r.status, "proved");
  });
});
