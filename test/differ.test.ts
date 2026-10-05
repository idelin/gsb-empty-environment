import { test } from "node:test";
import assert from "node:assert/strict";
import type { Query, Spec } from "../src/types.ts";
import { QueryError } from "../src/evaluate.ts";
import { findDifference, type DifferentResult, type EquivalentResult } from "../src/search.ts";

const rel = (name: string): Query => ({ op: "rel", name });
const sel = (input: Query, attr: string, value: number): Query => ({
  op: "select",
  input,
  predicate: { kind: "cmp", attr, cmp: "eq", value },
});
const proj = (input: Query, attributes: string[]): Query => ({
  op: "project",
  input,
  attributes,
});
const union = (left: Query, right: Query): Query => ({ op: "union", left, right });
const minus = (left: Query, right: Query): Query => ({ op: "minus", left, right });

function binRel(name: string, attrs: string[]) {
  return { name, attributes: attrs.map((n) => ({ name: n, domain: [0, 1] })) };
}

function asDifferent(r: EquivalentResult | DifferentResult): DifferentResult {
  assert.equal(r.kind, "different");
  return r as DifferentResult;
}

test("选择可交换: σa=1(σb=0(R)) ≡ σb=0(σa=1(R)),且穷举全部数据库", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a", "b"])],
    maxRows: 4,
    queries: { Q1: sel(sel(rel("R"), "b", 0), "a", 1), Q2: sel(sel(rel("R"), "a", 1), "b", 0) },
  };
  const r = findDifference(spec);
  assert.equal(r.kind, "equivalent");
  const eq = r as EquivalentResult;
  assert.equal(eq.databasesChecked, 16);
  assert.equal(eq.exhaustive, true);
});

test("投影与差集不交换: πa(R−S) ≢ πa(R)−πa(S),最小反例 2 行", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a", "b"]), binRel("S", ["a", "b"])],
    maxRows: 4,
    queries: {
      Q1: proj(minus(rel("R"), rel("S")), ["a"]),
      Q2: minus(proj(rel("R"), ["a"]), proj(rel("S"), ["a"])),
    },
  };
  const r = asDifferent(findDifference(spec));
  assert.equal(r.totalRows, 2);
  const rRows = r.database.find((d) => d.rel === "R")?.rows;
  const sRows = r.database.find((d) => d.rel === "S")?.rows;
  assert.deepEqual(rRows, [[0, 0]]);
  assert.deepEqual(sRows, [[0, 1]]);
  assert.deepEqual(r.onlyInQ1, [[0]]);
  assert.deepEqual(r.onlyInQ2, []);
  assert.deepEqual(r.q1.rows, [[0]]);
  assert.deepEqual(r.q2.rows, []);
  // 来源行: 数据库内的 R(0,0) 影响 Q1,S(0,1) 影响 Q2
  const inDbSources = r.differences[0]?.sourceRows.filter((s) => s.inDatabase);
  assert.equal(inDbSources?.length, 2);
  const rSrc = inDbSources?.find((s) => s.rel === "R");
  const sSrc = inDbSources?.find((s) => s.rel === "S");
  assert.deepEqual(rSrc?.tuple, [0, 0]);
  assert.equal(rSrc?.affectsQ1, true);
  assert.deepEqual(sSrc?.tuple, [0, 1]);
  assert.equal(sSrc?.affectsQ2, true);
});

test("并集可交换: R∪S ≡ S∪R", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a"]), binRel("S", ["a"])],
    maxRows: 3,
    queries: { Q1: union(rel("R"), rel("S")), Q2: union(rel("S"), rel("R")) },
  };
  const r = findDifference(spec);
  assert.equal(r.kind, "equivalent");
});

test("差集恒等式: R−(S∪T) ≡ (R−S)−T", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a"]), binRel("S", ["a"]), binRel("T", ["a"])],
    maxRows: 3,
    queries: {
      Q1: minus(rel("R"), union(rel("S"), rel("T"))),
      Q2: minus(minus(rel("R"), rel("S")), rel("T")),
    },
  };
  const r = findDifference(spec);
  assert.equal(r.kind, "equivalent");
  assert.equal((r as EquivalentResult).databasesChecked, 1 + 6 + 15 + 20);
});

test("差集不对称: R−S ≢ S−R,最小反例 1 行且字典序最小", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a"]), binRel("S", ["a"])],
    maxRows: 4,
    queries: { Q1: minus(rel("R"), rel("S")), Q2: minus(rel("S"), rel("R")) },
  };
  const r = asDifferent(findDifference(spec));
  assert.equal(r.totalRows, 1);
  assert.deepEqual(r.database.find((d) => d.rel === "R")?.rows, [[0]]);
  assert.deepEqual(r.database.find((d) => d.rel === "S")?.rows, []);
  assert.deepEqual(r.onlyInQ1, [[0]]);
  assert.deepEqual(r.onlyInQ2, []);
});

test("结果模式不同: 空数据库即可区分", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a", "b"])],
    maxRows: 2,
    queries: { Q1: proj(rel("R"), ["a"]), Q2: rel("R") },
  };
  const r = asDifferent(findDifference(spec));
  assert.equal(r.totalRows, 0);
  assert.ok(r.note && r.note.includes("模式不同"));
});

test("属性间比较谓词: σa=b(R) ≡ σb=a(R)", () => {
  const spec: Spec = {
    relations: [binRel("R", ["a", "b"])],
    maxRows: 4,
    queries: {
      Q1: { op: "select", input: rel("R"), predicate: { kind: "cmp", attr: "a", cmp: "eq", attr2: "b" } },
      Q2: { op: "select", input: rel("R"), predicate: { kind: "cmp", attr: "b", cmp: "eq", attr2: "a" } },
    },
  };
  assert.equal(findDifference(spec).kind, "equivalent");
});

test("搜索空间超限时报错", () => {
  const spec: Spec = {
    relations: [
      { name: "R", attributes: [{ name: "a", domain: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] }] },
    ],
    maxRows: 5,
    queries: { Q1: rel("R"), Q2: rel("R") },
  };
  assert.throws(() => findDifference(spec, { maxDatabases: 10 }), QueryError);
});

test("非法 spec: 空域报错", () => {
  const spec: Spec = {
    relations: [{ name: "R", attributes: [{ name: "a", domain: [] }] }],
    maxRows: 1,
    queries: { Q1: rel("R"), Q2: rel("R") },
  };
  assert.throws(() => findDifference(spec), QueryError);
});
