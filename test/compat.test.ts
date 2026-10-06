import { test } from "node:test";
import assert from "node:assert/strict";
import {
  checkCompatibility,
  enumerateValues,
  minValue,
  t,
  validate,
  valueSize,
} from "../src/index.ts";
import type { Schema } from "../src/index.ts";

/**
 * 交叉核对：在旧模式的有限枚举域里穷举所有被新模式拒绝的记录，
 * 验证 (a) 兼容性结论一致；(b) 返回的 witness 合法、被拒且规模最小。
 */
function crossCheck(oldS: Schema, newS: Schema, options?: { allowExtraFields?: boolean }) {
  const result = checkCompatibility(oldS, newS, options);
  const rejected = enumerateValues(oldS).filter((v) => !validate(newS, v, options));
  if (result.compatible) {
    assert.deepEqual(rejected, [], "枚举域内发现了反例，但判定为兼容");
    return;
  }
  assert.ok(rejected.length > 0, "判定不兼容，但枚举域内找不到反例");
  assert.ok(validate(oldS, result.witness, options), "witness 必须被旧模式接受");
  assert.ok(!validate(newS, result.witness, options), "witness 必须被新模式拒绝");
  const minSize = Math.min(...rejected.map(valueSize));
  assert.equal(valueSize(result.witness), minSize, "witness 必须是最小反例");
}

test("相同模式兼容", () => {
  const s = t.object({
    id: t.req(t.number()),
    role: t.req(t.enum(["admin", "user"])),
    tags: t.opt(t.array(t.string(), { maxLen: 4 })),
  });
  assert.deepEqual(checkCompatibility(s, s), { compatible: true });
  crossCheck(s, s);
});

test("枚举收窄：反例是被删除的枚举值", () => {
  const oldS = t.enum(["a", "b", "c"]);
  const newS = t.enum(["a", "c"]);
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) {
    assert.equal(r.witness, "b");
    assert.match(r.reason, /removed/);
  }
  crossCheck(oldS, newS);
});

test("枚举放宽：兼容", () => {
  const oldS = t.enum(["a", "b"]);
  const newS = t.enum(["a", "b", "c"]);
  assert.equal(checkCompatibility(oldS, newS).compatible, true);
  crossCheck(oldS, newS);
});

test("枚举 ⊆ string 兼容；string → enum 不兼容且反例为最短串", () => {
  const e = t.enum(["x", "y"]);
  assert.equal(checkCompatibility(e, t.string()).compatible, true);
  const r = checkCompatibility(t.string(), t.enum(["", "a"]));
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.equal(r.witness, "b"); // "" 和 "a" 都在枚举内
  crossCheck(t.string(), t.enum(["", "a"]));
  crossCheck(e, t.string());
});

test("新增必填字段不可忽略：反例是缺省该字段的最小旧记录", () => {
  const oldS = t.object({ id: t.req(t.number()) });
  const newS = t.object({ id: t.req(t.number()), name: t.req(t.string()) });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) {
    assert.deepEqual(r.witness, { id: 0 });
    assert.equal(r.path, "$.name");
    assert.match(r.reason, /new required field/);
  }
  crossCheck(oldS, newS);
});

test("可选变必填同样不兼容", () => {
  const oldS = t.object({ name: t.opt(t.string()) });
  const newS = t.object({ name: t.req(t.string()) });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, {});
  crossCheck(oldS, newS);
});

test("新增可选字段：兼容", () => {
  const oldS = t.object({ id: t.req(t.number()) });
  const newS = t.object({ id: t.req(t.number()), nick: t.opt(t.string()) });
  assert.equal(checkCompatibility(oldS, newS).compatible, true);
  crossCheck(oldS, newS);
});

test("可空被移除：反例是 null", () => {
  const oldS = t.nullable(t.string());
  const newS = t.string();
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.equal(r.witness, null);
  crossCheck(oldS, newS);
});

test("新增可空：兼容；可空内部收窄：不兼容", () => {
  assert.equal(checkCompatibility(t.string(), t.nullable(t.string())).compatible, true);
  const oldS = t.nullable(t.enum(["a", "b"]));
  const newS = t.nullable(t.enum(["a"]));
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.equal(r.witness, "b");
  crossCheck(t.string(), t.nullable(t.string()));
  crossCheck(oldS, newS);
});

test("数组长度下界抬高：反例是旧 minLen 的短数组", () => {
  const oldS = t.array(t.string(), { minLen: 0 });
  const newS = t.array(t.string(), { minLen: 2 });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, []);
  crossCheck(oldS, newS);
});

test("数组长度上界压低：反例是 newMax+1 的长数组", () => {
  const oldS = t.array(t.number(), { minLen: 1, maxLen: 5 });
  const newS = t.array(t.number(), { minLen: 1, maxLen: 2 });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, [0, 0, 0]);
  crossCheck(oldS, newS);
});

test("数组元素收窄：反例是装入元素反例的最短数组", () => {
  const oldS = t.array(t.enum(["red", "blue"]), { minLen: 1 });
  const newS = t.array(t.enum(["red"]), { minLen: 1 });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) {
    assert.deepEqual(r.witness, ["blue"]);
    assert.equal(r.path, "$[]");
  }
  crossCheck(oldS, newS);
});

test("嵌套对象字段收窄：反例带正确路径", () => {
  const oldS = t.object({
    meta: t.req(t.object({ level: t.req(t.enum([1, 2, 3])) })),
  });
  const newS = t.object({
    meta: t.req(t.object({ level: t.req(t.enum([1, 2])) })),
  });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) {
    assert.deepEqual(r.witness, { meta: { level: 3 } });
    assert.equal(r.path, "$.meta.level");
  }
  crossCheck(oldS, newS);
});

test("嵌套数组中的对象新增必填字段", () => {
  const oldS = t.object({
    items: t.req(t.array(t.object({ id: t.req(t.number()) }), { minLen: 1 })),
  });
  const newS = t.object({
    items: t.req(
      t.array(t.object({ id: t.req(t.number()), tag: t.req(t.string()) }), { minLen: 1 }),
    ),
  });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, { items: [{ id: 0 }] });
  crossCheck(oldS, newS);
});

test("可选字段携带坏值：反例包含该可选字段", () => {
  const oldS = t.object({ id: t.req(t.number()), flag: t.opt(t.enum(["y", "n"])) });
  const newS = t.object({ id: t.req(t.number()), flag: t.opt(t.enum(["y"])) });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, { id: 0, flag: "n" });
  crossCheck(oldS, newS);
});

test("多个失配点时返回规模最小的反例", () => {
  // 失配点 1：新增必填长数组字段（反例记录缺它，规模小）
  // 失配点 2：枚举收窄（反例需要构造带坏值的记录，规模大）
  const oldS = t.object({ e: t.req(t.enum(["a", "b"])) });
  const newS = t.object({
    e: t.req(t.enum(["a"])),
    extra: t.req(t.array(t.number(), { minLen: 3 })),
  });
  const r = checkCompatibility(oldS, newS);
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, { e: "a" }); // 缺省 extra 的最小记录
  crossCheck(oldS, newS);
});

test("原始类型不匹配：反例是旧类型最小值", () => {
  const r = checkCompatibility(t.string(), t.number());
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.equal(r.witness, "");
  crossCheck(t.string(), t.number());
});

test("封闭对象模式：旧模式多出的字段产生反例", () => {
  const oldS = t.object({ id: t.req(t.number()), legacy: t.opt(t.string()) });
  const newS = t.object({ id: t.req(t.number()) });
  assert.equal(checkCompatibility(oldS, newS).compatible, true); // 默认开放
  const r = checkCompatibility(oldS, newS, { allowExtraFields: false });
  assert.equal(r.compatible, false);
  if (!r.compatible) assert.deepEqual(r.witness, { id: 0, legacy: "" });
  crossCheck(oldS, newS, { allowExtraFields: false });
});

test("空枚举域与任何模式兼容（无合法记录）", () => {
  assert.equal(checkCompatibility(t.enum([]), t.number()).compatible, true);
});

test("minValue 构造的值必然合法", () => {
  const s = t.object({
    a: t.req(t.nullable(t.enum(["k"]))),
    b: t.opt(t.array(t.boolean(), { minLen: 2 })),
  });
  assert.ok(validate(s, minValue(s)));
});
