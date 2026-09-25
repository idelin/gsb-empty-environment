import { test } from "node:test";
import assert from "node:assert/strict";
import { PolicyError, evaluateCond, makeAttrs, validateCond, type Cond } from "../src/policy.ts";

test("有限集合、整数条件与布尔运算", () => {
  const attrs = makeAttrs({ region: "cn-sh", level: 3, vip: false });
  const cond: Cond = {
    op: "and",
    args: [
      { op: "in", attr: "region", set: ["cn-sh", "cn-bj"] },
      { op: "or", args: [{ op: "ge", attr: "level", value: 3 }, { op: "eq", attr: "vip", value: 1 }] },
      { op: "not", arg: { op: "lt", attr: "level", value: 0 } },
    ],
  };
  assert.equal(evaluateCond(cond, attrs), true);
  assert.equal(evaluateCond({ op: "in", attr: "region", set: ["us-west"] }, attrs), false);
  assert.equal(evaluateCond({ op: "le", attr: "level", value: 2 }, attrs), false);
  assert.equal(evaluateCond({ op: "ne", attr: "level", value: 3 }, attrs), false);
});

test("拒绝非法条件：非整数、未知运算、非法集合成员", () => {
  assert.throws(() => validateCond({ op: "ge", attr: "level", value: 1.5 }), (e: unknown) => (e as PolicyError).message.includes("整数"));
  assert.throws(
    () => validateCond({ op: "regex", attr: "region", pattern: ".*" }),
    (e: unknown) => (e as PolicyError).message.includes("不支持的运算"),
  );
  assert.throws(() => validateCond({ op: "in", attr: "r", set: [{ nested: 1 }] }), PolicyError);
  assert.throws(() => validateCond({ op: "in", attr: "r", set: "not-array" }), PolicyError);
  assert.throws(() => validateCond({ op: "and", args: [] }), PolicyError);
});

test("属性只读且显式：写入报错，读未声明属性报错", () => {
  const attrs = makeAttrs({ region: "cn-sh" });
  assert.throws(() => {
    (attrs as Record<string, unknown>).region = "us-west";
  }, (e: unknown) => (e as PolicyError).message.includes("只读"));
  assert.throws(
    () => evaluateCond({ op: "in", attr: "department", set: ["eng"] }, attrs),
    (e: unknown) => (e as PolicyError).message.includes("未声明的属性"),
  );
});

test("整数条件作用于非整数属性时报错", () => {
  const attrs = makeAttrs({ region: "cn-sh" });
  assert.throws(
    () => evaluateCond({ op: "gt", attr: "region", value: 1 }, attrs),
    (e: unknown) => (e as PolicyError).message.includes("不是整数"),
  );
});

test("不接外部身份服务：求值仅依赖显式传入的属性，纯同步完成", () => {
  const attrs = makeAttrs({ sub: "u-1", level: 5 });
  const cond: Cond = { op: "and", args: [{ op: "eq", attr: "level", value: 5 }, { op: "in", attr: "sub", set: ["u-1", "u-2"] }] };
  const result = evaluateCond(cond, attrs);
  assert.equal(result, true);
  assert.ok(typeof result === "boolean", "同步返回布尔值，无任何异步/外部调用");
});
