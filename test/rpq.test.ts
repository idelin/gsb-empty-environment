import { strict as assert } from "assert";
import {
  Automaton,
  Graph,
  QueryResult,
  compareEdgeIdSequences,
  enumeratePaths,
  findPath,
  productStateKey,
  replayEvidence,
  verifyQueryResult,
} from "../src/index";
import { EdgeId } from "../src/types";

let failures = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log("ok - " + name);
  } catch (err) {
    failures++;
    console.error("FAIL - " + name);
    console.error(err);
  }
}

function expectVerified(
  graph: Graph,
  automaton: Automaton,
  startNode: number,
  result: QueryResult | null,
  disabledEdges?: ReadonlySet<EdgeId>
): void {
  const check = verifyQueryResult(graph, automaton, startNode, result, disabledEdges);
  assert.ok(check.ok, "independent verification failed: " + check.error);
}

test("empty path: accepting initial state yields a zero-edge match", () => {
  const graph = new Graph({
    nodeCount: 2,
    edges: [{ id: 1, from: 0, to: 1, label: "a" }],
  });
  const automaton = new Automaton({
    stateCount: 1,
    initialStates: [0],
    acceptingStates: [0],
    transitions: [{ from: 0, label: "a", to: 0 }],
  });
  const result = findPath(graph, automaton, 0);
  assert.ok(result !== null);
  assert.equal(result.edgeCount, 0);
  assert.deepEqual(result.evidence.edgeIds, []);
  assert.equal(result.evidence.states.length, 1);
  assert.deepEqual(result.evidence.states[0], { node: 0, automatonState: 0 });
  expectVerified(graph, automaton, 0, result);
});

test("same node, different automaton states: self-loops drive the automaton", () => {
  const graph = new Graph({
    nodeCount: 1,
    edges: [
      { id: 1, from: 0, to: 0, label: "a" },
      { id: 2, from: 0, to: 0, label: "b" },
    ],
  });
  const automaton = new Automaton({
    stateCount: 3,
    initialStates: [0],
    acceptingStates: [2],
    transitions: [
      { from: 0, label: "a", to: 1 },
      { from: 1, label: "b", to: 2 },
      { from: 0, label: "b", to: 0 },
    ],
  });
  const result = findPath(graph, automaton, 0);
  assert.ok(result !== null);
  assert.deepEqual(result.evidence.edgeIds, [1, 2]);
  // Every product state stays on graph node 0 but the automaton state moves.
  assert.deepEqual(
    result.evidence.states.map((s) => s.node),
    [0, 0, 0]
  );
  assert.deepEqual(
    result.evidence.states.map((s) => s.automatonState),
    [0, 1, 2]
  );
  expectVerified(graph, automaton, 0, result);
});

test("unreachable: missing label and disconnected graph both yield null", () => {
  const graph = new Graph({
    nodeCount: 3,
    edges: [{ id: 1, from: 0, to: 1, label: "a" }],
  });
  const noLabel = new Automaton({
    stateCount: 2,
    initialStates: [0],
    acceptingStates: [1],
    transitions: [{ from: 0, label: "z", to: 1 }],
  });
  assert.equal(findPath(graph, noLabel, 0), null);
  expectVerified(graph, noLabel, 0, null);

  const disconnected = new Automaton({
    stateCount: 2,
    initialStates: [0],
    acceptingStates: [1],
    transitions: [{ from: 0, label: "a", to: 1 }],
  });
  // Node 2 is isolated; the only edge leaves node 0.
  assert.equal(findPath(graph, disconnected, 2), null);
  expectVerified(graph, disconnected, 2, null);
});

test("cycles in graph and automaton: search terminates with the shortest loop", () => {
  const graph = new Graph({
    nodeCount: 2,
    edges: [
      { id: 1, from: 0, to: 1, label: "a" },
      { id: 2, from: 1, to: 0, label: "a" },
    ],
  });
  const automaton = new Automaton({
    stateCount: 2,
    initialStates: [0],
    acceptingStates: [1],
    transitions: [
      { from: 0, label: "a", to: 1 },
      { from: 1, label: "a", to: 0 },
    ],
  });
  const result = findPath(graph, automaton, 0);
  assert.ok(result !== null);
  assert.deepEqual(result.evidence.edgeIds, [1]);
  expectVerified(graph, automaton, 0, result);
});

test("disabled edges: blocked best path falls back, fully blocked is null", () => {
  const graph = new Graph({
    nodeCount: 2,
    edges: [
      { id: 1, from: 0, to: 1, label: "a" },
      { id: 2, from: 0, to: 1, label: "a" },
    ],
  });
  const automaton = new Automaton({
    stateCount: 2,
    initialStates: [0],
    acceptingStates: [1],
    transitions: [{ from: 0, label: "a", to: 1 }],
  });
  const best = findPath(graph, automaton, 0);
  assert.ok(best !== null);
  assert.deepEqual(best.evidence.edgeIds, [1]);

  const disabled = new Set<EdgeId>([1]);
  const fallback = findPath(graph, automaton, 0, disabled);
  assert.ok(fallback !== null);
  assert.deepEqual(fallback.evidence.edgeIds, [2]);
  expectVerified(graph, automaton, 0, fallback, disabled);

  const all = new Set<EdgeId>([1, 2]);
  assert.equal(findPath(graph, automaton, 0, all), null);
  expectVerified(graph, automaton, 0, null, all);

  // Replaying evidence that uses a disabled edge must be rejected.
  const replay = replayEvidence(graph, automaton, best.evidence, disabled);
  assert.equal(replay.ok, false);
});

test("tie-break: lexicographic edge id order decides among equal lengths", () => {
  const graph = new Graph({
    nodeCount: 4,
    edges: [
      { id: 5, from: 0, to: 1, label: "a" },
      { id: 1, from: 1, to: 3, label: "a" },
      { id: 2, from: 0, to: 2, label: "a" },
      { id: 9, from: 2, to: 3, label: "a" },
    ],
  });
  const automaton = new Automaton({
    stateCount: 3,
    initialStates: [0],
    acceptingStates: [2],
    transitions: [
      { from: 0, label: "a", to: 1 },
      { from: 1, label: "a", to: 2 },
    ],
  });
  const result = findPath(graph, automaton, 0);
  assert.ok(result !== null);
  // [2, 9] beats [5, 1] even though 1 < 2, because comparison is lexicographic.
  assert.deepEqual(result.evidence.edgeIds, [2, 9]);
  expectVerified(graph, automaton, 0, result);
});

test("fewest edges wins over smaller edge ids", () => {
  const graph = new Graph({
    nodeCount: 3,
    edges: [
      { id: 100, from: 0, to: 2, label: "c" },
      { id: 1, from: 0, to: 1, label: "a" },
      { id: 2, from: 1, to: 2, label: "a" },
    ],
  });
  const automaton = new Automaton({
    stateCount: 3,
    initialStates: [0],
    acceptingStates: [1],
    transitions: [
      { from: 0, label: "c", to: 1 },
      { from: 0, label: "a", to: 2 },
      { from: 2, label: "a", to: 1 },
    ],
  });
  const result = findPath(graph, automaton, 0);
  assert.ok(result !== null);
  // [100] has one edge; [1, 2] has smaller ids but needs two edges.
  assert.deepEqual(result.evidence.edgeIds, [100]);
  expectVerified(graph, automaton, 0, result);
});

test("enumeratePaths yields best-first order and respects the limit", () => {
  const graph = new Graph({
    nodeCount: 5,
    edges: [
      { id: 1, from: 0, to: 1, label: "a" },
      { id: 2, from: 0, to: 2, label: "a" },
      { id: 3, from: 1, to: 3, label: "a" },
      { id: 4, from: 2, to: 3, label: "a" },
      { id: 5, from: 3, to: 4, label: "a" },
    ],
  });
  const automaton = new Automaton({
    stateCount: 3,
    initialStates: [0],
    acceptingStates: [2],
    transitions: [
      { from: 0, label: "a", to: 1 },
      { from: 1, label: "a", to: 2 },
      { from: 2, label: "a", to: 2 },
    ],
  });
  const all = enumeratePaths(graph, automaton, 0, 10);
  const sequences = all.map((r) => r.evidence.edgeIds);
  assert.deepEqual(sequences, [[1, 3], [2, 4], [1, 3, 5], [2, 4, 5]]);
  for (let i = 1; i < sequences.length; i++) {
    assert.ok(compareEdgeIdSequences(sequences[i - 1], sequences[i]) < 0);
  }
  const limited = enumeratePaths(graph, automaton, 0, 2);
  assert.deepEqual(limited.map((r) => r.evidence.edgeIds), [[1, 3], [2, 4]]);
});

// ---------------------------------------------------------------------------
// Randomized cross-check against a brute-force enumeration of all simple
// paths in the product graph.
// ---------------------------------------------------------------------------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function bruteForceBest(
  graph: Graph,
  automaton: Automaton,
  startNode: number,
  disabledEdges: ReadonlySet<EdgeId>
): EdgeId[] | null {
  let best: EdgeId[] | null = null;
  const visited = new Set<string>();
  const consider = (seq: EdgeId[]): void => {
    if (best === null || compareEdgeIdSequences(seq, best) < 0) best = seq.slice();
  };
  const dfs = (node: number, q: number, seq: EdgeId[]): void => {
    const key = productStateKey({ node, automatonState: q });
    if (visited.has(key)) return;
    visited.add(key);
    if (automaton.isAccepting(q)) consider(seq);
    for (const edge of graph.outgoingFrom(node, disabledEdges)) {
      for (const q2 of automaton.next(q, edge.label)) {
        dfs(edge.to, q2, seq.concat(edge.id));
      }
    }
    visited.delete(key);
  };
  for (const q0 of automaton.initialStates()) dfs(startNode, q0, []);
  return best;
}

function bruteForceAll(
  graph: Graph,
  automaton: Automaton,
  startNode: number,
  disabledEdges: ReadonlySet<EdgeId>
): EdgeId[][] {
  const found: EdgeId[][] = [];
  const visited = new Set<string>();
  const dfs = (node: number, q: number, seq: EdgeId[]): void => {
    const key = productStateKey({ node, automatonState: q });
    if (visited.has(key)) return;
    visited.add(key);
    if (automaton.isAccepting(q)) found.push(seq.slice());
    for (const edge of graph.outgoingFrom(node, disabledEdges)) {
      for (const q2 of automaton.next(q, edge.label)) {
        dfs(edge.to, q2, seq.concat(edge.id));
      }
    }
    visited.delete(key);
  };
  for (const q0 of automaton.initialStates()) dfs(startNode, q0, []);
  found.sort(compareEdgeIdSequences);
  return found;
}

test("randomized cross-check: findPath matches brute force and verification", () => {
  const rand = mulberry32(20261005);
  const pick = (n: number): number => Math.floor(rand() * n);
  const labels = ["a", "b"];
  for (let iter = 0; iter < 300; iter++) {
    const nodeCount = 1 + pick(5);
    const edgeCount = pick(9);
    const edges = [];
    const usedIds = new Set<number>();
    for (let i = 0; i < edgeCount; i++) {
      let id = pick(20);
      while (usedIds.has(id)) id = pick(20);
      usedIds.add(id);
      edges.push({
        id,
        from: pick(nodeCount),
        to: pick(nodeCount),
        label: labels[pick(labels.length)],
      });
    }
    const stateCount = 1 + pick(3);
    const transitions = [];
    for (let i = 0; i < pick(8); i++) {
      transitions.push({
        from: pick(stateCount),
        label: labels[pick(labels.length)],
        to: pick(stateCount),
      });
    }
    const graph = new Graph({ nodeCount, edges });
    const automaton = new Automaton({
      stateCount,
      initialStates: [pick(stateCount)],
      acceptingStates: [pick(stateCount)],
      transitions,
    });
    const startNode = pick(nodeCount);
    const disabledEdges = new Set<EdgeId>();
    for (const id of usedIds) {
      if (rand() < 0.25) disabledEdges.add(id);
    }

    const result = findPath(graph, automaton, startNode, disabledEdges);
    const expected = bruteForceBest(graph, automaton, startNode, disabledEdges);
    const context =
      "iter " + iter + " edges=" + JSON.stringify(edges) +
      " transitions=" + JSON.stringify(transitions) +
      " disabled=" + JSON.stringify(Array.from(disabledEdges));
    if (expected === null) {
      assert.equal(result, null, "expected unreachable: " + context);
    } else {
      assert.ok(result !== null, "expected a path: " + context);
      assert.deepEqual(result.evidence.edgeIds, expected, "wrong best path: " + context);
    }
    const check = verifyQueryResult(graph, automaton, startNode, result, disabledEdges);
    assert.ok(check.ok, "verification failed: " + check.error + " " + context);

    const enumerated = enumeratePaths(graph, automaton, startNode, 1000, disabledEdges)
      .map((r) => r.evidence.edgeIds);
    assert.deepEqual(
      enumerated,
      bruteForceAll(graph, automaton, startNode, disabledEdges),
      "enumeration mismatch: " + context
    );
  }
});

if (failures > 0) {
  console.error(failures + " test(s) failed");
  process.exit(1);
}
console.log("all tests passed");
