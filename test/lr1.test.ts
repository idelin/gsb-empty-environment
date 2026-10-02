/**
 * LR(1) 分析表构建与冲突检测诊断器 —— Node.js 内置测试（node:test）。
 *
 * 运行：npm test
 * 或：  node --experimental-strip-types --test test/lr1.test.ts
 */

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  parseGrammar,
  computeFirst,
  computeFollow,
  computeMinYield,
  buildAutomaton,
  buildTables,
  resolveConflicts,
  diagnoseConflicts,
  closure,
  formatConflictReport,
  END,
  type Grammar,
} from "../src/index.ts";

const EXPR_GRAMMAR = `
E -> E + T | T
T -> T * F | F
F -> ( E ) | id
`;

const DANGLING_ELSE = `
S -> if E then S else S
S -> if E then S
S -> other
E -> cond
`;

const REDUCE_REDUCE = `
S -> A | B
A -> x
B -> x
`;

function sorted(s: Set<string>): string[] {
  return [...s].sort();
}

test("文法解析：终结符/非终结符分类与开始符号", () => {
  const g = parseGrammar(DANGLING_ELSE);
  assert.equal(g.start, "S");
  assert.deepEqual(sorted(g.nonterminals), ["E", "S"]);
  assert.deepEqual(sorted(g.terminals), ["cond", "else", "if", "other", "then"]);
  assert.equal(g.productions.length, 4);
});

test("First 集：表达式文法", () => {
  const g = parseGrammar(EXPR_GRAMMAR);
  const first = computeFirst(g);
  assert.deepEqual(sorted(first.get("E")!), ["(", "id"]);
  assert.deepEqual(sorted(first.get("T")!), ["(", "id"]);
  assert.deepEqual(sorted(first.get("F")!), ["(", "id"]);
});

test("First 集：含 ε 产生式", () => {
  const g = parseGrammar(`
S -> A B
A -> a | ε
B -> b | ε
`);
  const first = computeFirst(g);
  assert.deepEqual(sorted(first.get("A")!), ["a", "ε"]);
  assert.deepEqual(sorted(first.get("B")!), ["b", "ε"]);
  assert.deepEqual(sorted(first.get("S")!), ["a", "b", "ε"]);
});

test("Follow 集：表达式文法", () => {
  const g = parseGrammar(EXPR_GRAMMAR);
  const first = computeFirst(g);
  const follow = computeFollow(g, first);
  assert.deepEqual(sorted(follow.get("E")!), ["$", ")", "+"]);
  assert.deepEqual(sorted(follow.get("T")!), ["$", ")", "*", "+"]);
  assert.deepEqual(sorted(follow.get("F")!), ["$", ")", "*", "+"]);
});

test("LR(1) 闭包：初始项集包含期望的项与前瞻符号", () => {
  const g = parseGrammar(DANGLING_ELSE);
  const automaton = buildAutomaton(g);
  const ag = automaton.grammar;
  // 增广文法产生式: 0: S' -> S, 1: S -> if E then S else S, 2: S -> if E then S, 3: S -> other, 4: E -> cond
  const items = closure([{ prod: 0, dot: 0, lookahead: END }], ag, automaton.first);
  const has = (prod: number, dot: number, lookahead: string) =>
    items.some((it) => it.prod === prod && it.dot === dot && it.lookahead === lookahead);
  assert.ok(has(0, 0, END), "应包含 S' -> ·S [$]");
  assert.ok(has(1, 0, END), "应包含 S -> ·if E then S else S [$]");
  assert.ok(has(2, 0, END), "应包含 S -> ·if E then S [$]");
  assert.ok(has(3, 0, END), "应包含 S -> ·other [$]");
});

test("Dangling-Else：恰好检测出 1 处 Shift/Reduce 冲突，触发 token 为 else", () => {
  const g = parseGrammar(DANGLING_ELSE);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);

  assert.equal(tables.conflicts.length, 1);
  const conflict = tables.conflicts[0];
  assert.equal(conflict.type, "shift/reduce");
  assert.equal(conflict.token, "else");

  // 冲突单元格中移进与归约动作同时存在
  const kinds = conflict.actions.map((a) => a.kind).sort();
  assert.deepEqual(kinds, ["reduce", "shift"]);

  // 引发冲突的项：S -> if E then S · else S（移进）与 S -> if E then S ·（归约）
  const ag = automaton.grammar;
  const shiftItem = conflict.items.find((it) => {
    const p = ag.productions[it.prod];
    return it.dot < p.rhs.length && p.rhs[it.dot] === "else";
  });
  const reduceItem = conflict.items.find((it) => {
    const p = ag.productions[it.prod];
    return it.dot === p.rhs.length && it.lookahead === "else";
  });
  assert.ok(shiftItem, "应存在 else 上的移进项");
  assert.ok(reduceItem, "应存在 else 上的归约项");
  assert.equal(ag.productions[reduceItem!.prod].rhs.join(" "), "if E then S");
});

test("Dangling-Else：输出最短输入 token 串证据并通过模拟验证", () => {
  const g = parseGrammar(DANGLING_ELSE);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);
  const diagnoses = diagnoseConflicts(automaton, tables);

  assert.equal(diagnoses.length, 1);
  const d = diagnoses[0];
  // 规范 LR(1) 中冲突状态的归约项前瞻必须含 else，即内层 S 之后须紧跟 else，
  // 因此最短证据需要两层 if：
  //   if cond then if cond then other else
  // （读完前 7 个 token 后处于冲突状态，下一个 token else 上移进/归约均合法）
  assert.deepEqual(d.evidence, [
    "if",
    "cond",
    "then",
    "if",
    "cond",
    "then",
    "other",
    "else",
  ]);
  assert.equal(d.evidence[d.evidence.length - 1], "else");
  assert.ok(d.verified, "证据应通过非确定性模拟验证");
  assert.ok(d.suggestions.length >= 3, "应给出多条解决建议");
});

test("Dangling-Else：冲突报告包含状态、动作、证据与解决建议", () => {
  const g = parseGrammar(DANGLING_ELSE);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);
  const [d] = diagnoseConflicts(automaton, tables);
  const report = formatConflictReport(d, automaton);

  assert.match(report, /Shift\/Reduce 冲突/);
  assert.match(report, /触发 token: else/);
  assert.match(report, /shift \d+/);
  assert.match(report, /reduce S -> if E then S/);
  assert.match(report, /if cond then if cond then other else/);
  assert.match(report, /解决建议/);
  assert.match(report, /SHIFT 优先/);
});

test("Dangling-Else：resolveConflicts('shift') 选择移进（else 绑定最近的 if）", () => {
  const g = parseGrammar(DANGLING_ELSE);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);
  const resolved = resolveConflicts(tables, "shift");
  const c = tables.conflicts[0];
  const chosen = resolved.action.get(c.state)!.get(c.token)!;
  assert.equal(chosen.length, 1);
  assert.equal(chosen[0].kind, "shift");
});

test("Reduce/Reduce 冲突：准确定位并给出最短证据", () => {
  const g = parseGrammar(REDUCE_REDUCE);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);

  assert.equal(tables.conflicts.length, 1);
  const conflict = tables.conflicts[0];
  assert.equal(conflict.type, "reduce/reduce");
  assert.equal(conflict.token, END);
  assert.equal(conflict.actions.filter((a) => a.kind === "reduce").length, 2);

  const [d] = diagnoseConflicts(automaton, tables);
  assert.deepEqual(d.evidence, ["x"]);
  assert.ok(d.verified);
  assert.match(d.suggestions[0], /无法用优先级/);
});

test("LR(1) 表达式文法：无冲突且含 accept 动作", () => {
  const g = parseGrammar(EXPR_GRAMMAR);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);
  assert.equal(tables.conflicts.length, 0);
  const hasAccept = [...tables.action.values()].some((row) =>
    [...row.values()].some((cell) => cell.some((a) => a.kind === "accept")),
  );
  assert.ok(hasAccept, "ACTION 表应包含 accept 动作");
});

test("minYield：每个符号可推导出的最短终结符串", () => {
  const g: Grammar = parseGrammar(DANGLING_ELSE);
  const minYield = computeMinYield(g);
  assert.deepEqual(minYield.get("S"), ["other"]);
  assert.deepEqual(minYield.get("E"), ["cond"]);
  assert.deepEqual(minYield.get("if"), ["if"]);
});

test("证据最短性：证据长度等于到冲突状态的最短输入长度", () => {
  // 构造一个需要更长前缀才能触发冲突的文法：
  // Follow(T) = { c }，读完 "a A x" 后，遇 c 时 T -> x 可归约、T -> x · c 可移进。
  // 冲突状态的可行前缀为 "a A x"，其中 A 最短推出 "b b"，故最短证据为 a b b x c。
  const g = parseGrammar(`
S -> a A T c
T -> x
T -> x c
A -> b b
`);
  const automaton = buildAutomaton(g);
  const tables = buildTables(automaton);
  assert.equal(tables.conflicts.length, 1);
  const conflict = tables.conflicts[0];
  assert.equal(conflict.type, "shift/reduce");
  assert.equal(conflict.token, "c");
  const [d] = diagnoseConflicts(automaton, tables);
  assert.deepEqual(d.evidence, ["a", "b", "b", "x", "c"]);
  assert.ok(d.verified);
});
