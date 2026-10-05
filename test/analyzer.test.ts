import { test } from "node:test";
import assert from "node:assert/strict";
import { analyze } from "../src/analyzer.ts";
import type { AnalysisResult } from "../src/analyzer.ts";
import { parseGraph } from "../src/parser.ts";

function run(def: unknown): AnalysisResult {
  return analyze(parseGraph(def));
}

function pathIds(result: AnalysisResult): string[] {
  return (result.path ?? []).map((step) => step.nodeId);
}

test("assignment propagates taint from source to sink", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "assign", dst: "q", src: "input" },
      { id: "n3", kind: "sink", var: "q", accepts: ["sql"], label: "db.query" },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
    ],
  });
  assert.equal(result.found, true);
  assert.deepEqual(pathIds(result), ["n1", "n2", "n3"]);
  assert.deepEqual(result.taintAtSink, ["sql"]);
  assert.equal(result.sinkLabel, "db.query");
});

test("constant assignment clears taint", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "const", var: "input" },
      { id: "n3", kind: "sink", var: "input", accepts: ["sql"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
    ],
  });
  assert.equal(result.found, false);
});

test("sanitizer removes only the listed taint types", () => {
  const graph = {
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql", "xss"] },
      { id: "n2", kind: "sanitize", dst: "safe", src: "input", types: ["xss"] },
      { id: "n3", kind: "sink", var: "safe", accepts: ["sql"], label: "db.query" },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
    ],
  };
  const sql = run(graph);
  assert.equal(sql.found, true);
  assert.deepEqual(sql.taintAtSink, ["sql"]);

  const xssOnly = structuredClone(graph) as { nodes: Array<{ accepts?: string[] }> };
  xssOnly.nodes[2]!.accepts = ["xss"];
  assert.equal(run(xssOnly).found, false);
});

test("alias write re-taints a sanitized value", () => {
  const graph = {
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "assign", dst: "a", src: "input" },
      { id: "n3", kind: "sanitize", dst: "a", src: "a", types: ["sql"] },
      { id: "n4", kind: "alias", dst: "p", src: "a" },
      { id: "n5", kind: "assign", dst: "p", src: "input" },
      { id: "n6", kind: "sink", var: "a", accepts: ["sql"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
      { from: "n3", to: "n4" },
      { from: "n4", to: "n5" },
      { from: "n5", to: "n6" },
    ],
  };
  assert.equal(run(graph).found, true);

  // Without the write through the alias, the sanitized value stays clean.
  const noWrite = structuredClone(graph) as {
    nodes: Array<{ id: string; kind: string }>;
    edges: Array<{ from: string; to: string }>;
  };
  noWrite.nodes = noWrite.nodes.filter((node) => node.id !== "n5");
  noWrite.edges = noWrite.edges
    .filter((edge) => edge.from !== "n4" && edge.to !== "n6" && edge.from !== "n5")
    .concat([{ from: "n4", to: "n6" }]);
  assert.equal(run(noWrite).found, false);
});

test("reads through an alias see the aliased taint", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["cmd"] },
      { id: "n2", kind: "alias", dst: "p", src: "input" },
      { id: "n3", kind: "sink", var: "p", accepts: ["cmd"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
    ],
  });
  assert.equal(result.found, true);
});

test("contradictory boolean conditions make a branch infeasible", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["cmd"] },
      { id: "n2", kind: "branch" },
      { id: "n3", kind: "branch" },
      { id: "n4", kind: "assign", dst: "c", src: "input" },
      { id: "n5", kind: "sink", var: "c", accepts: ["cmd"] },
      { id: "n6", kind: "const", var: "c" },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3", cond: { var: "flag", value: true } },
      { from: "n3", to: "n4", cond: { var: "flag", value: false } },
      { from: "n4", to: "n5" },
      { from: "n2", to: "n6", cond: { var: "flag", value: false } },
      { from: "n6", to: "n5" },
    ],
  });
  // The tainted route needs flag == true and flag == false at once.
  assert.equal(result.found, false);
});

test("boolSet pins a condition variable and prunes the opposite edge", () => {
  const graph = {
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "boolSet", var: "flag", value: true },
      { id: "n3", kind: "branch" },
      { id: "n4", kind: "assign", dst: "q", src: "input" },
      { id: "n5", kind: "const", var: "q" },
      { id: "n6", kind: "sink", var: "q", accepts: ["sql"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
      { from: "n3", to: "n4", cond: { var: "flag", value: false } },
      { from: "n3", to: "n5", cond: { var: "flag", value: true } },
      { from: "n4", to: "n6" },
      { from: "n5", to: "n6" },
    ],
  };
  // flag is pinned to true, so the tainted n4 route is infeasible.
  assert.equal(run(graph).found, false);

  const flipped = structuredClone(graph) as { nodes: Array<{ kind: string; value?: boolean }> };
  const boolSet = flipped.nodes.find((node) => node.kind === "boolSet");
  boolSet!.value = false;
  const result = run(flipped);
  assert.equal(result.found, true);
  assert.deepEqual(result.constraints, { flag: false });
});

test("shortest of several feasible flows is reported", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "branch" },
      { id: "n3", kind: "assign", dst: "a", src: "input" },
      { id: "n4", kind: "assign", dst: "b", src: "a" },
      { id: "n5", kind: "assign", dst: "q", src: "b" },
      { id: "n6", kind: "assign", dst: "q", src: "input" },
      { id: "n7", kind: "sink", var: "q", accepts: ["sql"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3", cond: { var: "long", value: true } },
      { from: "n3", to: "n4" },
      { from: "n4", to: "n5" },
      { from: "n5", to: "n7" },
      { from: "n2", to: "n6", cond: { var: "long", value: false } },
      { from: "n6", to: "n7" },
    ],
  });
  assert.equal(result.found, true);
  assert.deepEqual(pathIds(result), ["n1", "n2", "n6", "n7"]);
  assert.deepEqual(result.constraints, { long: false });
});

test("merge point: taint from either incoming branch reaches the sink", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "branch" },
      { id: "n3", kind: "assign", dst: "a", src: "input" },
      { id: "n4", kind: "const", var: "a" },
      { id: "n5", kind: "sink", var: "a", accepts: ["sql"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3", cond: { var: "tainted", value: true } },
      { from: "n2", to: "n4", cond: { var: "tainted", value: false } },
      { from: "n3", to: "n5" },
      { from: "n4", to: "n5" },
    ],
  });
  assert.equal(result.found, true);
  assert.deepEqual(pathIds(result), ["n1", "n2", "n3", "n5"]);
  assert.deepEqual(result.constraints, { tainted: true });
});

test("loops terminate and still yield the shortest flow", () => {
  const result = run({
    entry: "n1",
    nodes: [
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "assign", dst: "a", src: "input" },
      { id: "n3", kind: "sink", var: "a", accepts: ["sql"] },
    ],
    edges: [
      { from: "n1", to: "n2" },
      { from: "n2", to: "n2" },
      { from: "n2", to: "n3" },
    ],
  });
  assert.equal(result.found, true);
  assert.deepEqual(pathIds(result), ["n1", "n2", "n3"]);
});

test("reported path starts at the originating source", () => {
  const result = run({
    entry: "n0",
    nodes: [
      { id: "n0", kind: "nop" },
      { id: "n1", kind: "source", var: "input", taints: ["sql"] },
      { id: "n2", kind: "sink", var: "input", accepts: ["sql"] },
    ],
    edges: [
      { from: "n0", to: "n1" },
      { from: "n1", to: "n2" },
    ],
  });
  assert.equal(result.found, true);
  assert.deepEqual(pathIds(result), ["n1", "n2"]);
});

test("parser rejects malformed graphs", () => {
  assert.throws(() => parseGraph({ entry: "x", nodes: [], edges: [] }), /entry/);
  assert.throws(
    () =>
      parseGraph({
        entry: "n1",
        nodes: [{ id: "n1", kind: "assign", dst: "a" }],
        edges: [],
      }),
    /src/,
  );
  assert.throws(
    () =>
      parseGraph({
        entry: "n1",
        nodes: [{ id: "n1", kind: "nop" }],
        edges: [{ from: "n1", to: "nope" }],
      }),
    /unknown node/,
  );
});
