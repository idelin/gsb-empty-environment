import { test } from "node:test";
import assert from "node:assert/strict";
import {
  END,
  makeGrammar,
  computeFirst,
  computeFollow,
} from "../src/grammar.ts";
import { buildLR1Table } from "../src/lr1.ts";
import { conflictEvidence, diagnose } from "../src/diagnose.ts";
import { danglingElseGrammar } from "../src/demo.ts";

test("First 集计算（Dangling-Else）", () => {
  const g = danglingElseGrammar();
  const first = computeFirst(g);
  assert.deepEqual([...first.get("S")!].sort(), ["if", "other"]);
  assert.deepEqual([...first.get("E")!], ["e"]);
});

test("Follow 集计算（Dangling-Else）", () => {
  const g = danglingElseGrammar();
  const follow = computeFollow(g, computeFirst(g));
  assert.deepEqual([...follow.get("S")!].sort(), [END, "else"].sort());
  assert.deepEqual([...follow.get("E")!], ["then"]);
});

test("含空产生式文法的 First/Follow", () => {
  const g = makeGrammar("A", [
    ["A", "B", "c"],
    ["B", "b"],
    ["B", "ε"],
  ]);
  const first = computeFirst(g);
  const follow = computeFollow(g, first);
  assert.deepEqual([...first.get("B")!].sort(), ["b", "ε"]);
  assert.deepEqual([...first.get("A")!].sort(), ["b", "c"]);
  assert.deepEqual([...follow.get("B")!], ["c"]);
});

test("Dangling-Else：检测到且仅检测到 else 上的 Shift/Reduce 冲突", () => {
  const table = buildLR1Table(danglingElseGrammar());
  assert.equal(table.conflicts.length, 1);
  const c = table.conflicts[0];
  assert.equal(c.kind, "shift/reduce");
  assert.equal(c.token, "else");
  const kinds = c.actions.map((a) => a.type).sort();
  assert.deepEqual(kinds, ["reduce", "shift"]);
});

test("Dangling-Else：冲突状态项目集包含两个核心项", () => {
  const table = buildLR1Table(danglingElseGrammar());
  const state = table.states[table.conflicts[0].state];
  const prods = new Set(state.map((it) => it.prod));
  // 产生式编号 0 为拓广文法的 S' -> S，用户产生式从 1 开始
  assert.ok(prods.has(1), "应含 S -> if E then S • else S");
  assert.ok(prods.has(2), "应含 S -> if E then S •");
});

test("Dangling-Else：最短冲突证据输入串", () => {
  const table = buildLR1Table(danglingElseGrammar());
  const evidence = conflictEvidence(table, table.conflicts[0]);
  assert.deepEqual(evidence, [
    "if", "e", "then", "if", "e", "then", "other", "else",
  ]);
});

test("Dangling-Else：诊断报告内容完整", () => {
  const table = buildLR1Table(danglingElseGrammar());
  const report = diagnose(table);
  assert.match(report, /shift\/reduce 冲突/);
  assert.match(report, /向前看符号 'else'/);
  assert.match(report, /shift -> state \d+/);
  assert.match(report, /reduce by S -> if E then S/);
  assert.match(report, /最短冲突证据输入串/);
  assert.match(report, /解决建议/);
});

test("无二义表达式文法：无冲突", () => {
  const g = makeGrammar("E", [
    ["E", "E", "+", "T"],
    ["E", "T"],
    ["T", "T", "*", "F"],
    ["T", "F"],
    ["F", "(", "E", ")"],
    ["F", "id"],
  ]);
  const table = buildLR1Table(g);
  assert.deepEqual(table.conflicts, []);
  assert.match(diagnose(table), /未检测到冲突/);
});

test("Reduce/Reduce 冲突检测", () => {
  const g = makeGrammar("S", [
    ["S", "A"],
    ["S", "B"],
    ["A", "x"],
    ["B", "x"],
  ]);
  const table = buildLR1Table(g);
  const rr = table.conflicts.filter((c) => c.kind === "reduce/reduce");
  assert.equal(rr.length, 1);
  assert.equal(rr[0].token, END);
  const reduces = rr[0].actions.filter((a) => a.type === "reduce");
  assert.equal(reduces.length, 2);
  const evidence = conflictEvidence(table, rr[0]);
  assert.deepEqual(evidence, ["x", END]);
});
