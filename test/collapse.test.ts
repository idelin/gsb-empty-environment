import assert from "node:assert/strict";
import test from "node:test";
import { CollapseView, Graph } from "../src/index.ts";
import type { ViewSnapshot } from "../src/index.ts";

/**
 * Fixture tree:
 *
 *   r
 *   ├── a
 *   │   ├── a1
 *   │   └── a2
 *   └── b
 *       └── b1
 *
 * Edges:
 *   e1: a1 -> b1   e2: a2 -> b    e3: a1 -> a2 (internal to a)
 *   e4: a1 -> b1   (parallel to e1)
 *   e5: b1 -> a1   (reverse direction)
 *   e6: b  -> b    (self-loop on a visible node)
 *   e7: a1 -> a1   (self-loop inside a)
 */
function buildGraph(): Graph {
  const graph = new Graph();
  graph.addNode({ id: "r" });
  graph.addNode({ id: "a", parentId: "r" });
  graph.addNode({ id: "a1", parentId: "a" });
  graph.addNode({ id: "a2", parentId: "a" });
  graph.addNode({ id: "b", parentId: "r" });
  graph.addNode({ id: "b1", parentId: "b" });
  graph.addEdge({ id: "e1", source: "a1", target: "b1" });
  graph.addEdge({ id: "e2", source: "a2", target: "b" });
  graph.addEdge({ id: "e3", source: "a1", target: "a2" });
  graph.addEdge({ id: "e4", source: "a1", target: "b1" });
  graph.addEdge({ id: "e5", source: "b1", target: "a1" });
  graph.addEdge({ id: "e6", source: "b", target: "b" });
  graph.addEdge({ id: "e7", source: "a1", target: "a1" });
  return graph;
}

/**
 * Independent reference model: maps each ORIGINAL edge, one by one, through
 * the visible-ancestor function derived from the collapsed set. Written
 * separately from the library implementation so the two can be cross-checked.
 */
function referenceView(graph: Graph, collapsed: ReadonlySet<string>): ViewSnapshot {
  const hidden = (id: string): boolean => {
    let current = graph.parentOf(id);
    while (current !== null) {
      if (collapsed.has(current)) return true;
      current = graph.parentOf(current);
    }
    return false;
  };
  const visibleAncestor = (id: string): string => {
    let current = id;
    while (hidden(current)) current = graph.parentOf(current)!;
    return current;
  };

  const nodes = [];
  const counts = new Map<string, { hiddenNodeCount: number; hiddenEdgeCount: number }>();
  for (const id of graph.nodeIds()) {
    if (!hidden(id)) {
      counts.set(id, { hiddenNodeCount: 0, hiddenEdgeCount: 0 });
      nodes.push({
        id,
        collapsed: collapsed.has(id),
        hiddenNodeCount: 0,
        hiddenEdgeCount: 0,
      });
    }
  }
  for (const id of graph.nodeIds()) {
    if (hidden(id)) counts.get(visibleAncestor(id))!.hiddenNodeCount += 1;
  }

  const groups = new Map<string, { source: string; target: string; edgeIds: string[] }>();
  for (const edgeId of graph.edgeIds()) {
    const edge = graph.getEdge(edgeId);
    const source = visibleAncestor(edge.source);
    const target = visibleAncestor(edge.target);
    if (source === target && collapsed.has(source)) {
      counts.get(source)!.hiddenEdgeCount += 1;
      continue;
    }
    const key = `${source} -> ${target}`;
    let group = groups.get(key);
    if (!group) {
      group = { source, target, edgeIds: [] };
      groups.set(key, group);
    }
    group.edgeIds.push(edgeId);
  }

  for (const node of nodes) {
    const count = counts.get(node.id)!;
    node.hiddenNodeCount = count.hiddenNodeCount;
    node.hiddenEdgeCount = count.hiddenEdgeCount;
  }
  return { nodes, edges: [...groups.values()] };
}

function collapsedSet(view: CollapseView, ids: string[]): Set<string> {
  const set = new Set<string>();
  for (const id of ids) {
    if (view.isCollapsed(id)) set.add(id);
  }
  return set;
}

function edgeMap(snapshot: ViewSnapshot): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const edge of snapshot.edges) {
    map.set(`${edge.source} -> ${edge.target}`, edge.edgeIds);
  }
  return map;
}

function nodeById(snapshot: ViewSnapshot, id: string) {
  const node = snapshot.nodes.find((candidate) => candidate.id === id);
  assert.ok(node, `expected node ${id} in view`);
  return node;
}

test("collapsing a subtree maps cross-subtree edges to the visible ancestor", () => {
  const view = new CollapseView(buildGraph());
  view.collapse("a");
  const snapshot = view.getView();

  assert.deepEqual(
    snapshot.nodes.map((node) => node.id),
    ["r", "a", "b", "b1"],
  );

  const edges = edgeMap(snapshot);
  assert.deepEqual(edges.get("a -> b1"), ["e1", "e4"], "parallel edges keep every original id");
  assert.deepEqual(edges.get("a -> b"), ["e2"]);
  assert.deepEqual(edges.get("b1 -> a"), ["e5"], "direction is preserved");
  assert.deepEqual(edges.get("b -> b"), ["e6"], "self-loop on a visible node stays visible");
  assert.equal(edges.size, 4);

  const a = nodeById(snapshot, "a");
  assert.equal(a.collapsed, true);
  assert.equal(a.hiddenNodeCount, 2, "a1 and a2 are hidden");
  assert.equal(a.hiddenEdgeCount, 2, "e3 and e7 are internal to a");
});

test("nested collapse remaps edges to the outer collapsed node", () => {
  const view = new CollapseView(buildGraph());
  view.collapse("a");
  view.collapse("r");
  const snapshot = view.getView();

  assert.deepEqual(
    snapshot.nodes.map((node) => node.id),
    ["r"],
  );
  assert.equal(snapshot.edges.length, 0, "every edge is internal to r");
  const r = nodeById(snapshot, "r");
  assert.equal(r.hiddenNodeCount, 5);
  assert.equal(r.hiddenEdgeCount, 7, "all seven original edges are hidden inside r");
});

test("expanding restores original edges exactly, no reverse guessing", () => {
  const graph = buildGraph();
  const view = new CollapseView(graph);
  view.collapse("a");
  view.collapse("r");
  view.expand("r");

  // a is still collapsed: the view matches the single-collapse state again.
  let edges = edgeMap(view.getView());
  assert.deepEqual(edges.get("a -> b1"), ["e1", "e4"]);
  assert.deepEqual(edges.get("a -> b"), ["e2"]);
  assert.deepEqual(edges.get("b1 -> a"), ["e5"]);
  assert.deepEqual(edges.get("b -> b"), ["e6"]);

  view.expand("a");
  const snapshot = view.getView();
  assert.deepEqual(
    snapshot.nodes.map((node) => node.id),
    ["r", "a", "a1", "a2", "b", "b1"],
  );
  edges = edgeMap(snapshot);
  assertOriginalEdgeSet(graph, snapshot);
  for (const node of snapshot.nodes) {
    assert.equal(node.hiddenEdgeCount, 0);
    assert.equal(node.hiddenNodeCount, 0);
  }
});

/**
 * With nothing collapsed, every original edge is visible exactly once:
 * aggregates partition the original edge ids and each aggregate's endpoints
 * are the original endpoints of its edges (parallel edges share one aggregate).
 */
function assertOriginalEdgeSet(graph: Graph, snapshot: ViewSnapshot): void {
  const seen: string[] = [];
  for (const aggregated of snapshot.edges) {
    for (const edgeId of aggregated.edgeIds) {
      const edge = graph.getEdge(edgeId);
      assert.equal(aggregated.source, edge.source);
      assert.equal(aggregated.target, edge.target);
      seen.push(edgeId);
    }
  }
  assert.deepEqual([...seen].sort(), [...graph.edgeIds()].sort());
}

test("collapsing sibling subtrees aggregates edges between them", () => {
  const view = new CollapseView(buildGraph());
  view.collapse("a");
  view.collapse("b");
  const snapshot = view.getView();

  const edges = edgeMap(snapshot);
  assert.deepEqual(edges.get("a -> b"), ["e1", "e2", "e4"]);
  assert.deepEqual(edges.get("b -> a"), ["e5"]);
  assert.equal(edges.size, 2);
  assert.equal(nodeById(snapshot, "a").hiddenEdgeCount, 2, "e3, e7");
  assert.equal(nodeById(snapshot, "b").hiddenEdgeCount, 1, "e6 becomes internal to b");
});

test("collapse/expand are idempotent and validate ids", () => {
  const view = new CollapseView(buildGraph());
  view.collapse("a");
  view.collapse("a");
  assert.equal(view.getView().nodes.length, 4);
  view.expand("a");
  view.expand("a");
  assert.equal(view.getView().nodes.length, 6);
  assert.throws(() => view.collapse("nope"), /unknown node/);
  assert.throws(() => view.expand("nope"), /unknown node/);
  view.toggle("a");
  assert.equal(view.isCollapsed("a"), true);
  view.toggle("a");
  assert.equal(view.isCollapsed("a"), false);
});

test("collapsing a node inside an already collapsed subtree takes effect on expand", () => {
  const view = new CollapseView(buildGraph());
  view.collapse("r");
  view.collapse("a"); // hidden now, but recorded
  view.expand("r");
  assert.equal(view.isCollapsed("a"), true);
  assert.deepEqual(
    view.getView().nodes.map((node) => node.id),
    ["r", "a", "b", "b1"],
  );
});

test("library matches the per-edge mapping reference model across nested collapse, parallel edges and uncollapse", () => {
  const graph = buildGraph();
  const view = new CollapseView(graph);
  const allNodes = graph.nodeIds();

  // Seeded PRNG for a deterministic fuzz over collapse/expand operations.
  let state = 0x2f6e2b1;
  const random = () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };

  for (let step = 0; step < 500; step++) {
    const id = allNodes[Math.floor(random() * allNodes.length)];
    if (random() < 0.5) {
      view.collapse(id);
    } else {
      view.expand(id);
    }
    const expected = referenceView(graph, collapsedSet(view, allNodes));
    assert.deepEqual(view.getView(), expected, `view mismatch after step ${step}`);
  }
});

test("full expand after any fuzz sequence restores the original edge set", () => {
  const graph = buildGraph();
  const view = new CollapseView(graph);
  for (const id of graph.nodeIds()) view.collapse(id);
  for (const id of graph.nodeIds()) view.expand(id);

  const snapshot = view.getView();
  assertOriginalEdgeSet(graph, snapshot);
});
