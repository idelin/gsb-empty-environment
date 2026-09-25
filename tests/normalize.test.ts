import { describe, it } from "node:test";
import assert from "node:assert/strict";
import type { JsonValue, RuleSet } from "../src/types.ts";
import { applyInversePatch, isStable, normalize } from "../src/normalize.ts";

const ruleset: RuleSet = {
  name: "people",
  rules: [
    { id: "ws-name", type: "whitespace", path: "/people/*/name", mode: "collapse" },
    { id: "d-birth", type: "date", path: "/people/*/birth", formats: ["YYYY-MM-DD"], outputFormat: "DD/MM/YYYY" },
    { id: "e-sex", type: "enum", path: "/people/*/sex", map: { M: "male", F: "female" } },
  ],
};

const input: JsonValue = {
  people: [
    { name: "  Ada   Lovelace  ", birth: "1990-03-07", sex: "F", note: null, age: 30 },
    { name: "Bob", birth: "31/12/2021", sex: "Z" },
  ],
};

describe("规范化", () => {
  it("逐字段按显式顺序转换字符串，null 与数字不被等同处理", () => {
    const r = normalize(input, ruleset);
    assert.equal((r.output as any).people[0].name, "Ada Lovelace");
    assert.equal((r.output as any).people[0].birth, "07/03/1990");
    assert.equal((r.output as any).people[0].sex, "female");
    assert.strictEqual((r.output as any).people[0].note, null);
    assert.equal((r.output as any).people[0].age, 30);
    // 输入对象不被修改
    assert.equal((input as any).people[0].name, "  Ada   Lovelace  ");
  });

  it("出错保留原值并定位到具体路径与规则", () => {
    const r = normalize(input, ruleset);
    assert.equal((r.output as any).people[1].birth, "31/12/2021");
    assert.equal((r.output as any).people[1].sex, "Z");
    const paths = r.errors.map((e) => e.path).sort();
    assert.deepEqual(paths, ["/people/1/birth", "/people/1/sex"]);
    const dateErr = r.errors.find((e) => e.code === "DATE_UNPARSEABLE")!;
    assert.equal(dateErr.ruleId, "d-birth");
    assert.equal(dateErr.retained, "31/12/2021");
    assert.match(dateErr.message, /不匹配/);
    assert.equal(dateErr.path, "/people/1/birth");
  });

  it("逐字段变更记录：每步含规则 id、from、to", () => {
    const r = normalize(input, ruleset);
    const rec = r.records["/people/0/name"];
    assert.equal(rec.changes.length, 1);
    assert.deepEqual(rec.changes[0], {
      ruleId: "ws-name",
      ruleType: "whitespace",
      from: "  Ada   Lovelace  ",
      to: "Ada Lovelace",
    });
    assert.equal(rec.unchanged, false);
    assert.equal(rec.before.fingerprint, "str:  Ada   Lovelace  ");
    assert.equal(rec.after.fingerprint, "str:Ada Lovelace");
  });

  it("未变化字段（null/未知出错保留/本来相等）unchanged", () => {
    const r = normalize(input, ruleset);
    assert.equal(r.records["/people/0/note"].unchanged, true);
    assert.equal(r.records["/people/0/note"].before.kind, "null");
    assert.equal(r.records["/people/0/age"].before.kind, "other");
    assert.equal(r.records["/people/1/birth"].unchanged, true);
    assert.equal(r.records["/people/1/birth"].error?.code, "DATE_UNPARSEABLE");
  });

  it("规范化两次不继续改变（幂等）", () => {
    const r = normalize(input, ruleset);
    const second = normalize(r.output, ruleset);
    assert.equal(JSON.stringify(second.output), JSON.stringify(r.output));
    // 第二遍没有任何新变更
    assert.equal(Object.values(second.records).every((x) => x.changes.length === 0), true);
    assert.equal(isStable(input, ruleset), true);
  });

  it("逆补丁恢复规范化前的树（仅 replace，按序应用）", () => {
    const r = normalize(input, ruleset);
    const restored = applyInversePatch(r.output, r.inversePatch);
    assert.equal(JSON.stringify(restored), JSON.stringify(input));
    // 出错与未改的字段不出现在逆补丁里
    assert.deepEqual(r.inversePatch.map((op) => op.path).sort(), [
      "/people/0/birth",
      "/people/0/name",
      "/people/0/sex",
    ]);
  });

  it("规则顺序显式：空白先于枚举，带空白的别名被正确消费", () => {
    const ordered: RuleSet = {
      name: "o",
      rules: [
        { id: "w", type: "whitespace", path: "/s", mode: "trim" },
        { id: "e", type: "enum", path: "/s", map: { male: "MALE" } },
      ],
    };
    const r = normalize({ s: "  male  " } as JsonValue, ordered);
    assert.equal((r.output as any).s, "MALE");
    assert.deepEqual(r.records["/s"].changes.map((c) => c.ruleId), ["w", "e"]);
  });

  it("静态分析不通过时默认拒绝规范化", () => {
    const bad: RuleSet = {
      name: "bad",
      rules: [{ id: "e", type: "enum", path: "/s", map: { A: "B", B: "A" } }],
    };
    assert.throws(() => normalize({ s: "A" } as JsonValue, bad), /静态分析未通过/);
  });

  it("数组有序、对象键无序（遍历稳定，与键声明顺序无关）", () => {
    const a = normalize({ z: " x ", a: " y " } as JsonValue, {
      name: "k",
      rules: [{ id: "w", type: "whitespace", path: "/*", mode: "trim" }],
    });
    assert.equal((a.output as any).z, "x");
    assert.equal((a.output as any).a, "y");
    assert.deepEqual(Object.keys(a.records).sort(), ["/a", "/z"]);
  });
});
