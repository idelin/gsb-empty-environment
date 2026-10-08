import assert from "node:assert/strict";
import test from "node:test";
import { CollapsePreprocessor, mapEdgesPerEdge } from "../src/index.ts";
import type { Graph, VisibleGraph } from "../src/index.ts";

/**
 * Hierarchy used across the focused tests:
 *
 *   A ── B ── C
 *   │    └── D
 *   └── E
 *   X (root, outside A)
 */
const hierarchy: Graph = {
  nodes: [
    { id: "A" },
    { id: "B", parent: "A" },
    { id: "C", parent: "B" },
    { id: "D", parent: "B" },
    { id: "E", parent: "A" },
    { id: "X" },
  ],
  edges: [
    { id: "e1", source: "C", target: "X" },
    { id: "e2", source: "X", target: "D" },
    { id: "e3", source: "C", target: "D" },
    { id: "e4", source: "C", target: "E" },
    { id: "e5", source: "E", target: "X" },
  ],
};

test("nested collapse maps deep edges onto the outermost collapsed ancestor", () => {
  const pre = new CollapsePreprocessor(hierarchy);
  pre.collapse("B");
  pre.collapse("A"); // nested: B is hidden behind A

  const graph = pre.visibleGraph();
  assert.deepEqual(
    graph.nodes.map((n) => n.id),
    ["A", "X"],
  );
  const a = graph.nodes[0]!;
  assert.equal(a.collapsed, true);
  assert.equal(a.hiddenDescendants, 4); // B, C, D, E

  // e1 (C→X) and e5 (E→X) aggregate onto A→X; e2 (X→D) maps to X→A.
  const ax = graph.edges.find((e) => e.id === "A→X")!;
  assert.deepEqual(ax.edgeIds, ["e1", "e5"]);
  assert.equal(ax.count, 2);
  const xa = graph.edges.find((e) => e.id === "X→A")!;
  assert.deepEqual(xa.edgeIds, ["e2"]);

  // e3 (C→D) and e4 (C→E) are internal to A's subtree: hidden, counted on A.
  assert.equal(a.hiddenEdgeCount, 2);
  assert.deepEqual(a.hiddenEdgeIds, ["e3", "e4"]);

  // Matches the per-edge reference model exactly.
  assert.deepEqual(graph, mapEdgesPerEdge(hierarchy, ["A", "B"]));
});

test("parallel edges aggregate into one edge preserving every original id", () => {
  const graph: Graph = {
    nodes: [{ id: "P" }, { id: "u", parent: "P" }, { id: "v", parent: "P" }, { id: "w" }],
    edges: [
      { id: "p1", source: "u", target: "w" },
      { id: "p2", source: "v", target: "w" },
      { id: "p3", source: "u", target: "w" },
      { id: "p4", source: "w", target: "v" },
    ],
  };
  const pre = new CollapsePreprocessor(graph);
  pre.collapse("P");

  const visible = pre.visibleGraph();
  const pw = visible.edges.find((e) => e.id === "P→w")!;
  assert.deepEqual(pw.edgeIds, ["p1", "p2", "p3"]);
  assert.equal(pw.count, 3);
  // Direction matters: w→P is a separate aggregate.
  const wp = visible.edges.find((e) => e.id === "w→P")!;
  assert.deepEqual(wp.edgeIds, ["p4"]);
  assert.equal(visible.edges.length, 2);

  assert.deepEqual(visible, mapEdgesPerEdge(graph, ["P"]));
});

test("internal self-loops are hidden but counted; genuine self-loops stay visible", () => {
  const graph: Graph = {
    nodes: [{ id: "P" }, { id: "u", parent: "P" }, { id: "v", parent: "P" }, { id: "w" }],
    edges: [
      { id: "inner", source: "u", target: "v" }, // becomes internal when P collapses
      { id: "loop", source: "u", target: "u" }, // self-loop inside collapsed P
      { id: "outer", source: "w", target: "w" }, // genuine self-loop on visible w
    ],
  };
  const pre = new CollapsePreprocessor(graph);
  pre.collapse("P");

  const visible = pre.visibleGraph();
  const p = visible.nodes.find((n) => n.id === "P")!;
  assert.equal(p.hiddenEdgeCount, 2);
  assert.deepEqual(p.hiddenEdgeIds, ["inner", "loop"]);

  // The genuine self-loop on the visible node w survives as a normal edge.
  const loop = visible.edges.find((e) => e.id === "w→w")!;
  assert.deepEqual(loop.edgeIds, ["outer"]);

  assert.deepEqual(visible, mapEdgesPerEdge(graph, ["P"]));
});

test("expanding restores the original edges exactly, with no reverse guessing", () => {
  const pre = new CollapsePreprocessor(hierarchy);
  const baseline = pre.visibleGraph();

  pre.collapse("A");
  pre.collapse("B");
  pre.expand("A");
  pre.collapse("E");
  pre.expandAll();

  const restored = pre.visibleGraph();
  assert.deepEqual(restored, baseline);

  // Every original edge is back as its own singleton aggregate.
  assert.equal(restored.edges.length, hierarchy.edges.length);
  for (const edge of hierarchy.edges) {
    const aggregate = restored.edges.find((e) => e.id === `${edge.source}→${edge.target}`)!;
    assert.ok(aggregate.edgeIds.includes(edge.id));
  }
  for (const node of restored.nodes) {
    assert.equal(node.hiddenEdgeCount, 0);
    assert.equal(node.hiddenDescendants, 0);
  }
});

test("partial uncollapse of a nested collapse reveals the inner level only", () => {
  const pre = new CollapsePreprocessor(hierarchy);
  pre.collapse("A");
  pre.collapse("B");
  pre.expand("A"); // B stays collapsed, C/D/E now map to B or A?

  const visible = pre.visibleGraph();
  // A expanded: A, B, E, X visible; C and D hidden behind B.
  assert.deepEqual(
    visible.nodes.map((n) => n.id),
    ["A", "B", "E", "X"],
  );
  const bx = visible.edges.find((e) => e.id === "B→X")!;
  assert.deepEqual(bx.edgeIds, ["e1"]); // C→X now lands on B
  const xb = visible.edges.find((e) => e.id === "X→B")!;
  assert.deepEqual(xb.edgeIds, ["e2"]);

  assert.deepEqual(visible, mapEdgesPerEdge(hierarchy, ["B"]));
});

test("invalid graphs are rejected", () => {
  assert.throws(
    () => new CollapsePreprocessor({ nodes: [{ id: "a" }, { id: "a" }], edges: [] }),
    /Duplicate node id/,
  );
  assert.throws(
    () => new CollapsePreprocessor({ nodes: [{ id: "a", parent: "ghost" }], edges: [] }),
    /unknown parent/,
  );
  assert.throws(
    () =>
      new CollapsePreprocessor({
        nodes: [
          { id: "a", parent: "b" },
          { id: "b", parent: "a" },
        ],
        edges: [],
      }),
    /Cycle/,
  );
  assert.throws(
    () =>
      new CollapsePreprocessor({
        nodes: [{ id: "a" }],
        edges: [{ id: "e", source: "a", target: "ghost" }],
      }),
    /unknown node/,
  );
});

// --- Randomized cross-check against the per-edge reference model ---

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomGraph(rand: () => number, nodeCount: number, edgeCount: number): Graph {
  const nodes = [{ id: "n0" }];
  for (let i = 1; i < nodeCount; i += 1) {
    const parent = Math.floor(rand() * i); // random earlier node keeps it a forest
    nodes.push({ id: `n${i}`, parent: `n${parent}` });
  }
  const edges = [];
  for (let i = 0; i < edgeCount; i += 1) {
    edges.push({
      id: `e${i}`,
      source: `n${Math.floor(rand() * nodeCount)}`,
      target: `n${Math.floor(rand() * nodeCount)}`,
    });
  }
  return { nodes, edges };
}

function allEdgeIds(graph: VisibleGraph): string[] {
  const ids: string[] = [];
  for (const edge of graph.edges) {
    ids.push(...edge.edgeIds);
  }
  for (const node of graph.nodes) {
    ids.push(...node.hiddenEdgeIds);
  }
  return ids.sort();
}

test("random collapse/expand sequences always match the per-edge model", () => {
  const rand = mulberry32(20261008);
  for (let round = 0; round < 50; round += 1) {
    const graph = randomGraph(rand, 12, 30);
    const pre = new CollapsePreprocessor(graph);
    const collapsed = new Set<string>();

    for (let step = 0; step < 40; step += 1) {
      const id = `n${Math.floor(rand() * 12)}`;
      if (rand() < 0.5) {
        pre.collapse(id);
        collapsed.add(id);
      } else {
        pre.expand(id);
        collapsed.delete(id);
      }

      const actual = pre.visibleGraph();
      const expected = mapEdgesPerEdge(graph, collapsed);
      assert.deepEqual(actual, expected, `round ${round} step ${step}`);

      // Invariant: every original edge id appears exactly once, either in one
      // aggregate or in one node's hidden set — nothing lost, nothing invented.
      assert.deepEqual(
        allEdgeIds(actual),
        graph.edges.map((e) => e.id).sort(),
      );
    }

    // Full expansion always restores the pristine per-edge mapping.
    pre.expandAll();
    assert.deepEqual(pre.visibleGraph(), mapEdgesPerEdge(graph, []));
  }
});
