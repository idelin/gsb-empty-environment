import { test } from "node:test";
import assert from "node:assert/strict";
import { LineageStore, mergeEvidence, evidenceKey } from "../src/lineage.ts";
import type { Evidence } from "../src/types.ts";

const ev = (source: string, ...path: (string | number)[]): Evidence => ({ source, path });

test("mergeEvidence 按 (source, path) 去重且保序", () => {
  const merged = mergeEvidence(
    [ev("a", 0, "x"), ev("b", 0, "x")],
    [ev("a", 0, "x"), ev("a", 1, "x")],
  );
  assert.deepEqual(merged, [ev("a", 0, "x"), ev("b", 0, "x"), ev("a", 1, "x")]);
});

test("同值不同来源 => 不同证据键，绝不合并", () => {
  assert.notEqual(evidenceKey(ev("src1", 0, "v")), evidenceKey(ev("src2", 0, "v")));
  const merged = mergeEvidence([ev("src1", 0, "v")], [ev("src2", 0, "v")]);
  assert.equal(merged.length, 2);
});

test("LineageStore 记录去重：同版本同输出同证据集只留一条", () => {
  const s = new LineageStore();
  const v = s.beginStage("stage");
  s.record(v, [0, "total"], [ev("orders", 0, "amount")]);
  s.record(v, [0, "total"], [ev("orders", 0, "amount")]);
  assert.equal(s.query(v).length, 1);
});

test("按版本查询：精确版本与 asOf 累积", () => {
  const s = new LineageStore();
  const v1 = s.beginStage("s1");
  s.record(v1, [0, "a"], [ev("src", 0, "a")]);
  const v2 = s.beginStage("s2");
  s.record(v2, [0, "b"], [ev("src", 0, "b")]);
  s.record(v2, [0, "a"], [ev("src", 9, "a")]); // 覆盖 $[0].a

  assert.deepEqual(s.query(v1).map((r) => r.output), ["$[0].a"]);
  assert.deepEqual(s.query(v2).map((r) => r.output), ["$[0].a", "$[0].b"]);
  assert.equal(s.query().length, 3);

  const asOfV1 = s.asOf(v1);
  assert.deepEqual(asOfV1.map((r) => r.output), ["$[0].a"]);
  assert.deepEqual(asOfV1[0]!.evidence, [ev("src", 0, "a")]);

  const asOfV2 = s.asOf(v2);
  assert.equal(asOfV2.length, 2);
  assert.deepEqual(asOfV2.find((r) => r.output === "$[0].a")!.evidence, [ev("src", 9, "a")]);
});

test("explain 反查输出字段的证据并可限定版本", () => {
  const s = new LineageStore();
  const v1 = s.beginStage("s1");
  s.record(v1, [0, "t"], [ev("o", 0, "amt")]);
  const v2 = s.beginStage("s2");
  s.record(v2, [0, "t"], [ev("o", 1, "amt")]);
  assert.deepEqual(s.explain("$[0].t", v1), [ev("o", 0, "amt")]);
  assert.deepEqual(s.explain("$[0].t"), [ev("o", 0, "amt"), ev("o", 1, "amt")]);
});
