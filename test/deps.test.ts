import { test } from "node:test";
import assert from "node:assert/strict";
import { analyzeRules, dependencyClosure, dependencyEdges } from "../src/index.ts";
import type { Rule } from "../src/index.ts";

test("detects alias conflicts on overlapping write paths", () => {
  const rules: Rule[] = [
    { id: "e1", kind: "enum-alias", aliases: { na: "N/A" }, targets: [["status"]] },
    { id: "e2", kind: "enum-alias", aliases: { na: "Unknown" }, targets: [["status"]] },
  ];
  const { issues } = analyzeRules(rules);
  assert.equal(issues.length, 1);
  assert.equal(issues[0].type, "alias-conflict");
  assert.deepEqual(issues[0].ruleIds, ["e1", "e2"]);
});

test("no conflict when paths do not overlap", () => {
  const rules: Rule[] = [
    { id: "e1", kind: "enum-alias", aliases: { na: "N/A" }, targets: [["a"]] },
    { id: "e2", kind: "enum-alias", aliases: { na: "Unknown" }, targets: [["b"]] },
  ];
  assert.equal(analyzeRules(rules).issues.length, 0);
});

test("detects non-idempotent alias chains inside one rule", () => {
  const rules: Rule[] = [
    { id: "e", kind: "enum-alias", aliases: { a: "b", b: "c" }, targets: [["v"]] },
  ];
  const { issues } = analyzeRules(rules);
  assert.equal(issues.length, 1);
  assert.equal(issues[0].type, "non-idempotent-combo");
  assert.match(issues[0].message, /"a" -> "b" -> "c"/);
});

test("detects non-idempotent alias chains across rules", () => {
  const rules: Rule[] = [
    { id: "e1", kind: "enum-alias", aliases: { a: "b" }, targets: [["v"]] },
    { id: "e2", kind: "enum-alias", aliases: { b: "c" }, targets: [["v"]] },
  ];
  const { issues } = analyzeRules(rules);
  assert.ok(issues.some((i) => i.type === "non-idempotent-combo"));
});

test("detects mutual trigger cycles via read/write paths", () => {
  const rules: Rule[] = [
    { id: "c1", kind: "copy", from: ["a"], to: ["b"], targets: [] },
    { id: "c2", kind: "copy", from: ["b"], to: ["a"], targets: [] },
  ];
  const { issues, edges } = analyzeRules(rules);
  assert.equal(edges.length, 2);
  const cycles = issues.filter((i) => i.type === "trigger-cycle");
  assert.equal(cycles.length, 1);
  assert.deepEqual(cycles[0].ruleIds, ["c1", "c2"]);
});

test("detects whitespace-before-enum ordering hazard", () => {
  const rules: Rule[] = [
    { id: "w", kind: "whitespace", mode: "trim", targets: [["status"]] },
    { id: "e", kind: "enum-alias", aliases: { na: " N/A " }, targets: [["status"]] },
  ];
  const { issues } = analyzeRules(rules);
  assert.ok(issues.some((i) => i.type === "non-idempotent-combo"));
});

test("enum before whitespace with clean canonicals is fine", () => {
  const rules: Rule[] = [
    { id: "e", kind: "enum-alias", aliases: { na: "N/A" }, targets: [["status"]] },
    { id: "w", kind: "whitespace", mode: "trim", targets: [["status"]] },
  ];
  assert.equal(analyzeRules(rules).issues.length, 0);
});

test("dependency edges follow writes into reads", () => {
  const rules: Rule[] = [
    { id: "c", kind: "copy", from: ["src"], to: ["dst"], targets: [] },
    { id: "w", kind: "whitespace", mode: "trim", targets: [["dst"]] },
    { id: "e", kind: "enum-alias", aliases: { a: "A" }, targets: [["other"]] },
  ];
  const edges = dependencyEdges(rules);
  assert.ok(edges.some((e) => e.from === "c" && e.to === "w"));
  assert.ok(!edges.some((e) => e.from === "c" && e.to === "e"));
});

test("dependency closure captures transitive downstream rules", () => {
  const rules: Rule[] = [
    { id: "c", kind: "copy", from: ["src"], to: ["dst"], targets: [] },
    { id: "w", kind: "whitespace", mode: "trim", targets: [["dst"]] },
    { id: "e", kind: "enum-alias", aliases: { a: "A" }, targets: [["dst"]] },
    { id: "u", kind: "enum-alias", aliases: { x: "X" }, targets: [["unrelated"]] },
  ];
  assert.deepEqual(dependencyClosure(rules, ["c"]).sort(), ["c", "e", "w"]);
  assert.deepEqual(dependencyClosure(rules, ["u"]), ["u"]);
});
