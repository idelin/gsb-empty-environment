import { test } from "node:test";
import assert from "node:assert/strict";
import { trackSource, field, drill } from "../src/relation.ts";
import { filterRel, splitRel, joinRel, aggregateRel, canonical } from "../src/ops.ts";
import { PathError } from "../src/errors.ts";
import type { JSONValue } from "../src/types.ts";

const num = (row: any, name: string) => field(row, name, [name]).value as number;

test("canonical：对象键无序、数组有序", () => {
  assert.equal(
    canonical({ b: 1, a: { d: 2, c: 3 } }),
    canonical({ a: { c: 3, d: 2 }, b: 1 }),
  );
  assert.notEqual(canonical([1, 2]), canonical([2, 1]));
});

test("filter：被过滤的行不进入下游血缘", () => {
  const rel = trackSource("orders", [
    { id: 1, amount: 10 },
    { id: 2, amount: 999 },
  ]);
  const kept = filterRel(rel, (r) => num(r, "amount") < 100);
  assert.equal(kept.rows.length, 1);
  const ev = field(kept.rows[0]!, "amount", []).evidence;
  assert.deepEqual(ev, [{ source: "orders", path: [0, "amount"] }]);
});

test("split：分组保序，证据随行流动", () => {
  const rel = trackSource("s", [
    { k: "b", v: 1 },
    { k: "a", v: 2 },
    { k: "b", v: 3 },
  ]);
  const groups = splitRel(rel, (r) => field(r, "k", []).value as string);
  assert.deepEqual([...groups.keys()], ["b", "a"]);
  assert.equal(groups.get("b")!.rows.length, 2);
  assert.deepEqual(field(groups.get("b")!.rows[1]!, "v", []).evidence, [
    { source: "s", path: [2, "v"] },
  ]);
});

test("join：双侧证据并集；连接键同名时合并证据", () => {
  const left = trackSource("L", [{ id: 1, x: "lx" }]);
  const right = trackSource("R", [{ id: 1, y: "ry" }]);
  const joined = joinRel(left, right, { left: "id", right: "id" });
  assert.equal(joined.rows.length, 1);
  const row = joined.rows[0]!;
  assert.deepEqual(field(row, "id", []).evidence, [
    { source: "L", path: [0, "id"] },
    { source: "R", path: [0, "id"] },
  ]);
  assert.deepEqual(field(row, "y", []).evidence, [{ source: "R", path: [0, "y"] }]);
});

test("join：非键同名字段冲突 => 带路径的 PathError", () => {
  const left = trackSource("L", [{ id: 1, v: 1 }]);
  const right = trackSource("R", [{ id: 1, v: 2 }]);
  assert.throws(
    () => joinRel(left, right, { left: "id", right: "id" }),
    (err: unknown) => err instanceof PathError && err.message.includes("$[0].v"),
  );
});

test("aggregate：sum 证据覆盖全部贡献行", () => {
  const rel = trackSource("o", [
    { region: "cn", amount: 10 },
    { region: "cn", amount: 20 },
    { region: "us", amount: 5 },
  ]);
  const agg = aggregateRel(rel, {
    groupBy: ["region"],
    measures: { total: { op: "sum", field: "amount" }, n: { op: "count" } },
  });
  assert.equal(agg.rows.length, 2);
  const cn = agg.rows.find((r) => field(r, "region", []).value === "cn")!;
  assert.equal(field(cn, "total", []).value, 30);
  assert.deepEqual(field(cn, "total", []).evidence, [
    { source: "o", path: [0, "amount"] },
    { source: "o", path: [1, "amount"] },
  ]);
  assert.equal(field(cn, "n", []).value, 2);
  // count 的证据 = 贡献行全部字段证据的并集
  assert.equal(field(cn, "n", []).evidence.length, 4);
});

test("aggregate：同值不同来源不合并为同一证据", () => {
  const a = trackSource("A", [{ g: "x", v: 7 }]);
  const b = trackSource("B", [{ g: "x", v: 7 }]);
  const combined = { rows: [...a.rows, ...b.rows] };
  const agg = aggregateRel(combined, {
    groupBy: ["g"],
    measures: { total: { op: "sum", field: "v" } },
  });
  assert.equal(field(agg.rows[0]!, "total", []).value, 14);
  assert.deepEqual(field(agg.rows[0]!, "total", []).evidence, [
    { source: "A", path: [0, "v"] },
    { source: "B", path: [0, "v"] },
  ]);
});

test("aggregate：非数值字段报 PathError 并定位到来源路径", () => {
  const rel = trackSource("o", [{ amount: 1 }, { amount: "oops" as unknown as number }]);
  assert.throws(
    () => aggregateRel(rel, { measures: { t: { op: "sum", field: "amount" } } }),
    (err: unknown) =>
      err instanceof PathError &&
      err.source === "o" &&
      err.message.includes(`$[1].amount in source "o"`),
  );
});

test("drill：单证据精确延长路径；缺失段报 PathError", () => {
  const rel = trackSource("s", [{ nested: { list: [{ v: 42 }] } }]);
  const t = drill(field(rel.rows[0]!, "nested", []), ["list", 0, "v"], [0, "nested"]);
  assert.equal(t.value, 42);
  assert.deepEqual(t.evidence, [{ source: "s", path: [0, "nested", "list", 0, "v"] }]);
  assert.throws(
    () => drill(field(rel.rows[0]!, "nested", []), ["list", 5], [0, "nested"]),
    (err: unknown) => err instanceof PathError && err.message.includes("$[0].nested.list[5]"),
  );
});

test("trackSource：非对象行报错并定位来源路径", () => {
  assert.throws(
    () => trackSource("bad", [{ ok: 1 }, 7 as unknown as JSONValue]),
    (err: unknown) => err instanceof PathError && err.message.includes(`$[1] in source "bad"`),
  );
});
