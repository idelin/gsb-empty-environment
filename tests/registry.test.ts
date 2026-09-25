import { describe, it } from "node:test";
import assert from "node:assert/strict";
import type { JsonValue, RuleSet } from "../src/types.ts";
import { NormalizationSession, RuleRegistry } from "../src/registry.ts";

const goodV1: RuleSet = {
  name: "p",
  rules: [
    { id: "w", type: "whitespace", path: "/name", mode: "trim" },
    { id: "e", type: "enum", path: "/sex", map: { M: "male", F: "female" } },
  ],
};

const goodV2: RuleSet = {
  name: "p",
  rules: [
    { id: "w", type: "whitespace", path: "/name", mode: "collapse" },
    { id: "e", type: "enum", path: "/sex", map: { M: "male", F: "female" } },
  ],
};

const badCycle: RuleSet = {
  name: "p",
  rules: [
    { id: "e1", type: "enum", path: "/sex", map: { A: "B" } },
    { id: "e2", type: "enum", path: "/sex", map: { B: "C" } },
    { id: "e3", type: "enum", path: "/sex", map: { C: "B" } },
  ],
};

const badConflict: RuleSet = {
  name: "p",
  rules: [
    { id: "e1", type: "enum", path: "/sex", map: { M: "x" } },
    { id: "e2", type: "enum", path: "/sex", map: { M: "y" } },
  ],
};

describe("版本化发布", () => {
  it("合法且幂等的规则集发布成功并成为激活版本", () => {
    const reg = new RuleRegistry();
    const res = reg.publish("v1", goodV1);
    assert.equal(res.ok, true);
    assert.equal(res.promoted, true);
    assert.equal(reg.activeVersion, "v1");
    assert.equal(res.idempotency?.status, "proved");
  });

  it("非法规则发布失败：别名冲突，激活版本保持上一版", () => {
    const reg = new RuleRegistry();
    reg.publish("v1", goodV1);
    const res = reg.publish("v2-bad", badConflict);
    assert.equal(res.ok, false);
    assert.equal(res.promoted, false);
    assert.equal(reg.activeVersion, "v1");
    assert.equal(reg.getVersion("v2-bad"), null);
    assert.equal(reg.getRejected("v2-bad")?.reasons.length, 1);
    // 上一版结果仍可用
    assert.equal(reg.getActive()?.version, "v1");
  });

  it("非幂等规则（触发环）发布被拒，上一版本结果不被破坏", () => {
    const reg = new RuleRegistry();
    reg.publish("v1", goodV1);
    const before = JSON.stringify(reg.getActive()!.ruleset);
    const res = reg.publish("v2-cycle", badCycle);
    assert.equal(res.ok, false);
    // 静态环已足以拒绝；无论幂等搜索结论如何都不允许提升
    assert.ok(res.reasons.join(" ").includes("环") || res.idempotency?.status === "refuted");
    assert.equal(reg.activeVersion, "v1");
    assert.equal(JSON.stringify(reg.getActive()!.ruleset), before);
  });

  it("幂等性预算未决不得发布", () => {
    const reg = new RuleRegistry();
    const res = reg.publish("v?", goodV1, { budget: 0 });
    assert.equal(res.ok, false);
    assert.equal(reg.activeVersion, null);
    assert.match(res.reasons.join(" "), /未决|未证明/);
  });

  it("回滚到历史已发布版本", () => {
    const reg = new RuleRegistry();
    reg.publish("v1", goodV1);
    reg.publish("v2", goodV2);
    assert.equal(reg.activeVersion, "v2");
    assert.equal(reg.rollback("v1"), true);
    assert.equal(reg.activeVersion, "v1");
    assert.equal(reg.rollback("missing"), false);
  });
});

describe("局部规则更新只重算依赖字段", () => {
  it("v1->v2 只重算 /name，/sex 复用旧结果", () => {
    const reg = new RuleRegistry();
    reg.publish("v1", goodV1);
    reg.publish("v2", goodV2);

    const original: JsonValue = { name: "  a   b  ", sex: "M", untouched: 123 };
    const session = new NormalizationSession(reg, original, "v1");
    assert.equal(session.version, "v1");
    assert.equal((session.result.output as any).name, "a   b");

    const inc = session.updateTo("v2");
    assert.equal(inc.fromVersion, "v1");
    assert.equal(inc.toVersion, "v2");
    assert.deepEqual(inc.recomputedPaths, ["/name"]);
    assert.ok(inc.unchangedPaths.includes("/sex"));
    assert.ok(inc.unchangedPaths.includes("/untouched"));
    assert.equal((inc.output as any).name, "a b");
    assert.equal((inc.output as any).sex, "male");
    // 增量逆补丁仍能还原原始树
    const restored = JSON.parse(JSON.stringify(inc.output));
    for (const op of inc.inversePatch) {
      const segs = op.path.split("/").slice(1);
      let node: any = restored;
      for (let i = 0; i < segs.length - 1; i++) node = node[segs[i]];
      node[segs[segs.length - 1]] = op.value;
    }
    assert.equal(JSON.stringify(restored), JSON.stringify(original));
  });

  it("不能更新到未成功发布的版本", () => {
    const reg = new RuleRegistry();
    reg.publish("v1", goodV1);
    reg.publish("bad", badCycle);
    const session = new NormalizationSession(reg, { name: "x" } as JsonValue, "v1");
    assert.throws(() => session.updateTo("bad"), /未成功发布/);
    assert.equal(session.version, "v1");
  });

  it("新增针对新字段的规则：未命中该规则的字段不重算", () => {
    const reg = new RuleRegistry();
    const v1: RuleSet = { name: "p", rules: [{ id: "w", type: "whitespace", path: "/a", mode: "trim" }] };
    const v2: RuleSet = {
      name: "p",
      rules: [
        { id: "w", type: "whitespace", path: "/a", mode: "trim" },
        { id: "wb", type: "whitespace", path: "/b", mode: "trim" },
      ],
    };
    reg.publish("v1", v1);
    reg.publish("v2", v2);
    const original: JsonValue = { a: " x ", b: " y " };
    const session = new NormalizationSession(reg, original, "v1");
    const inc = session.updateTo("v2");
    assert.deepEqual(inc.recomputedPaths, ["/b"]);
    assert.equal((inc.output as any).b, "y");
  });
});
