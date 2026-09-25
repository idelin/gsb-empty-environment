import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import type { AttrDef, Policy, Rule } from "../src/model.ts";
import { evaluate, evalCondition } from "../src/evaluate.ts";
import { comparePolicies, enumerateInputs, validateAttrs } from "../src/compare.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const load = <T,>(rel: string): T =>
  JSON.parse(readFileSync(path.join(here, rel), "utf8")) as T;

const attrs = load<AttrDef[]>("../examples/attrs.json");
const oldPolicy = load<Policy>("../examples/old-policy.json");
const newPolicy = load<Policy>("../examples/new-policy.json");

test("条件求值：整数比较与布尔运算", () => {
  const input = { role: "dev", env: "prod", hour: 10 };
  assert.equal(
    evalCondition({ op: "between", attr: "hour", min: 9, max: 18 }, input),
    true,
  );
  assert.equal(
    evalCondition(
      {
        op: "and",
        args: [
          { op: "eq", attr: "role", value: "dev" },
          {
            op: "or",
            args: [
              { op: "lt", attr: "hour", value: 9 },
              { op: "ge", attr: "hour", value: 10 },
            ],
          },
          { op: "not", arg: { op: "eq", attr: "env", value: "staging" } },
        ],
      },
      input,
    ),
    true,
  );
  assert.equal(
    evalCondition({ op: "in", attr: "role", values: ["admin", "guest"] }, input),
    false,
  );
});

test("交换律：重排规则顺序不改变任何输入的判定与解释", () => {
  const shuffled: Policy = {
    ...oldPolicy,
    rules: [...oldPolicy.rules].reverse(),
  };
  const report = comparePolicies(attrs, oldPolicy, shuffled);
  assert.equal(report.counts["allow-to-deny"], 0);
  assert.equal(report.counts["deny-to-allow"], 0);
  assert.equal(report.counts["explanation-only"], 0);
  assert.equal(report.counts["unchanged"], report.totalInputs);
});

test("示例对比：allow→deny 单独成区且数量正确", () => {
  const report = comparePolicies(attrs, oldPolicy, newPolicy);
  assert.equal(report.totalInputs, 3 * 2 * 24);
  assert.equal(report.counts["allow-to-deny"], 3);
  const region = report.regions.find((r) => r.kind === "allow-to-deny");
  assert.ok(region);
  const keys = region.samples.map((s) => JSON.stringify(s.input)).sort();
  assert.deepEqual(keys, [
    '{"role":"admin","env":"prod","hour":22}',
    '{"role":"admin","env":"prod","hour":23}',
    '{"role":"dev","env":"prod","hour":18}',
  ]);
});

test("示例对比：deny→allow 与仅解释改变分别统计", () => {
  const report = comparePolicies(attrs, oldPolicy, newPolicy);
  assert.equal(report.counts["deny-to-allow"], 24);
  assert.equal(report.counts["explanation-only"], 59);
  assert.equal(report.counts["unchanged"], 58);
  const kinds = report.regions.map((r) => r.kind);
  assert.deepEqual(kinds, ["allow-to-deny", "deny-to-allow", "explanation-only"]);
});

test("仅解释改变：判定相同但命中理由不同", () => {
  const input = { role: "dev", env: "staging", hour: 3 };
  const oldRes = evaluate(oldPolicy, input);
  const newRes = evaluate(newPolicy, input);
  assert.equal(oldRes.decision, newRes.decision);
  assert.notDeepEqual(oldRes.reasons, newRes.reasons);
});

test("整数属性必须限定有限区间，否则拒绝分析", () => {
  const bad = [{ name: "hour", kind: "int" } as unknown as AttrDef];
  assert.throws(() => validateAttrs(bad), /有限闭区间/);
  assert.throws(
    () => validateAttrs([{ name: "h", kind: "int", min: 5, max: 1 }]),
    /有限闭区间/,
  );
});

test("穷举空间大小等于各属性域的笛卡尔积", () => {
  assert.equal(enumerateInputs(attrs).length, 144);
  const single: AttrDef[] = [{ name: "h", kind: "int", min: 0, max: 4 }];
  assert.equal(enumerateInputs(single).length, 5);
});

test("规则拆分后语义等价时无判定改变", () => {
  const merged: Rule = {
    id: "m1",
    effect: "allow",
    when: { op: "in", attr: "role", values: ["admin", "dev"] },
    reason: "管理员或开发",
  };
  const splitA: Rule = {
    id: "s1",
    effect: "allow",
    when: { op: "eq", attr: "role", value: "admin" },
    reason: "管理员或开发",
  };
  const splitB: Rule = {
    id: "s2",
    effect: "allow",
    when: { op: "eq", attr: "role", value: "dev" },
    reason: "管理员或开发",
  };
  const p1: Policy = {
    name: "p1",
    combining: "deny-overrides",
    default: "deny",
    rules: [merged],
  };
  const p2: Policy = { ...p1, name: "p2", rules: [splitA, splitB] };
  const roleOnly: AttrDef[] = [
    { name: "role", kind: "enum", values: ["admin", "dev", "guest"] },
  ];
  const report = comparePolicies(roleOnly, p1, p2);
  assert.equal(report.counts["allow-to-deny"], 0);
  assert.equal(report.counts["deny-to-allow"], 0);
  assert.equal(report.counts["explanation-only"], 2);
});
