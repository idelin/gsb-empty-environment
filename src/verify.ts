import { Automaton } from "./automaton";
import { Graph } from "./graph";
import { PathEvidence, QueryResult } from "./query";
import { EdgeId, NodeId, ProductState, productStateKey } from "./types";

export interface Verification {
  ok: boolean;
  error?: string;
}

function ok(): Verification {
  return { ok: true };
}

function fail(error: string): Verification {
  return { ok: false, error };
}

const NO_EDGES: ReadonlySet<EdgeId> = new Set<EdgeId>();

/**
 * Independently replays the evidence of a query result: every step must use a
 * real, non-disabled edge whose endpoints and label match the recorded
 * product-state transition, the trajectory must start at
 * (startNode, some initial automaton state) and end at an accepting state.
 */
export function replayEvidence(
  graph: Graph,
  automaton: Automaton,
  evidence: PathEvidence,
  disabledEdges?: ReadonlySet<EdgeId>
): Verification {
  const disabled = disabledEdges ?? NO_EDGES;
  if (evidence.states.length !== evidence.edgeIds.length + 1) {
    return fail(
      "evidence has " + evidence.states.length + " states for " +
      evidence.edgeIds.length + " edges"
    );
  }
  if (evidence.steps.length !== evidence.edgeIds.length) {
    return fail("step count does not match edge id count");
  }
  const first = evidence.states[0];
  if (first.node !== evidence.startNode) {
    return fail("evidence does not start at the query start node");
  }
  if (!automaton.isInitial(first.automatonState)) {
    return fail("evidence does not start in an initial automaton state");
  }
  for (let i = 0; i < evidence.edgeIds.length; i++) {
    const edgeId = evidence.edgeIds[i];
    const edge = graph.edge(edgeId);
    if (edge === undefined) return fail("unknown edge id: " + edgeId);
    if (disabled.has(edgeId)) return fail("edge " + edgeId + " is disabled");
    const from = evidence.states[i];
    const to = evidence.states[i + 1];
    if (edge.from !== from.node || edge.to !== to.node) {
      return fail("edge " + edgeId + " endpoints do not match the state trajectory");
    }
    if (automaton.next(from.automatonState, edge.label).indexOf(to.automatonState) === -1) {
      return fail(
        "automaton has no transition " + from.automatonState + " --" + edge.label +
        "--> " + to.automatonState
      );
    }
    const step = evidence.steps[i];
    if (step.edge.id !== edgeId) return fail("step " + i + " references the wrong edge");
    if (productStateKey(step.from) !== productStateKey(from) ||
        productStateKey(step.to) !== productStateKey(to)) {
      return fail("step " + i + " product states do not match the state trajectory");
    }
  }
  const last = evidence.states[evidence.states.length - 1];
  if (!automaton.isAccepting(last.automatonState)) {
    return fail("evidence does not end in an accepting automaton state");
  }
  return ok();
}

interface ProductEdge {
  fromKey: string;
  toKey: string;
  edgeId: EdgeId;
}

/**
 * Builds the full finite product graph (every graph edge combined with every
 * automaton transition on the same label). This is deliberately independent
 * of the search code in query.ts.
 */
function buildProductGraph(
  graph: Graph,
  automaton: Automaton,
  disabledEdges: ReadonlySet<EdgeId>
): { forward: Map<string, ProductEdge[]>; backward: Map<string, ProductEdge[]>; states: Set<string> } {
  const forward = new Map<string, ProductEdge[]>();
  const backward = new Map<string, ProductEdge[]>();
  const states = new Set<string>();
  const add = (map: Map<string, ProductEdge[]>, key: string, edge: ProductEdge): void => {
    let list = map.get(key);
    if (list === undefined) {
      list = [];
      map.set(key, list);
    }
    list.push(edge);
  };
  for (let node = 0; node < graph.nodeCount; node++) {
    for (let q = 0; q < automaton.stateCount; q++) {
      states.add(productStateKey({ node, automatonState: q }));
    }
  }
  for (const edge of graph.allEdges()) {
    if (disabledEdges.has(edge.id)) continue;
    for (let q = 0; q < automaton.stateCount; q++) {
      for (const q2 of automaton.next(q, edge.label)) {
        const pe: ProductEdge = {
          fromKey: productStateKey({ node: edge.from, automatonState: q }),
          toKey: productStateKey({ node: edge.to, automatonState: q2 }),
          edgeId: edge.id,
        };
        add(forward, pe.fromKey, pe);
        add(backward, pe.toKey, pe);
      }
    }
  }
  return { forward, backward, states };
}

function initialKeys(automaton: Automaton, startNode: NodeId): string[] {
  return automaton.initialStates().map((q) =>
    productStateKey({ node: startNode, automatonState: q })
  );
}

/**
 * Independently verifies that a query result is optimal, using a separate
 * finite product-graph BFS:
 *  1. reverse BFS from all accepting product states gives the exact minimum
 *     edge count of any matching path;
 *  2. a greedy pass over that distance field reconstructs the
 *     lexicographically smallest edge id sequence among shortest paths.
 * The result must match both. A null result is verified as "unreachable".
 */
export function verifyOptimality(
  graph: Graph,
  automaton: Automaton,
  startNode: NodeId,
  result: QueryResult | null,
  disabledEdges?: ReadonlySet<EdgeId>
): Verification {
  const disabled = disabledEdges ?? NO_EDGES;
  if (result !== null) {
    const replay = replayEvidence(graph, automaton, result.evidence, disabled);
    if (!replay.ok) return replay;
  }
  const { forward, backward } = buildProductGraph(graph, automaton, disabled);

  // 1. Exact distances to the nearest accepting product state.
  const dist = new Map<string, number>();
  const queue: string[] = [];
  for (let node = 0; node < graph.nodeCount; node++) {
    for (let q = 0; q < automaton.stateCount; q++) {
      if (automaton.isAccepting(q)) {
        const key = productStateKey({ node, automatonState: q });
        if (!dist.has(key)) {
          dist.set(key, 0);
          queue.push(key);
        }
      }
    }
  }
  for (let head = 0; head < queue.length; head++) {
    const key = queue[head];
    const d = dist.get(key) as number;
    for (const pe of backward.get(key) ?? []) {
      if (!dist.has(pe.fromKey)) {
        dist.set(pe.fromKey, d + 1);
        queue.push(pe.fromKey);
      }
    }
  }

  let minDist = Infinity;
  for (const key of initialKeys(automaton, startNode)) {
    const d = dist.get(key);
    if (d !== undefined && d < minDist) minDist = d;
  }

  if (result === null) {
    return minDist === Infinity
      ? ok()
      : fail("query reported unreachable but a matching path of " + minDist + " edges exists");
  }
  if (minDist === Infinity) {
    return fail("query returned a path but no matching path exists");
  }
  if (result.edgeCount !== minDist || result.evidence.edgeIds.length !== minDist) {
    return fail(
      "path has " + result.evidence.edgeIds.length + " edges but the minimum is " + minDist
    );
  }

  // 2. Greedy reconstruction of the lexicographically smallest shortest
  //    edge id sequence, keeping every product state the prefix can reach.
  let current = new Set<string>();
  for (const key of initialKeys(automaton, startNode)) {
    if (dist.get(key) === minDist) current.add(key);
  }
  const expected: EdgeId[] = [];
  for (let remaining = minDist; remaining > 0; remaining--) {
    let bestEdge: EdgeId | null = null;
    let nextSet = new Set<string>();
    for (const key of current) {
      for (const pe of forward.get(key) ?? []) {
        if (dist.get(pe.toKey) !== remaining - 1) continue;
        if (bestEdge === null || pe.edgeId < bestEdge) {
          bestEdge = pe.edgeId;
          nextSet = new Set<string>([pe.toKey]);
        } else if (pe.edgeId === bestEdge) {
          nextSet.add(pe.toKey);
        }
      }
    }
    if (bestEdge === null) {
      return fail("could not reconstruct a shortest path from the distance field");
    }
    expected.push(bestEdge);
    current = nextSet;
  }
  const actual = result.evidence.edgeIds;
  for (let i = 0; i < expected.length; i++) {
    if (actual[i] !== expected[i]) {
      return fail(
        "edge id sequence [" + actual.join(",") + "] is not the lexicographically " +
        "smallest shortest one [" + expected.join(",") + "]"
      );
    }
  }
  return ok();
}

/** Convenience: full independent check of a query outcome. */
export function verifyQueryResult(
  graph: Graph,
  automaton: Automaton,
  startNode: NodeId,
  result: QueryResult | null,
  disabledEdges?: ReadonlySet<EdgeId>
): Verification {
  return verifyOptimality(graph, automaton, startNode, result, disabledEdges);
}

/** Re-export for convenience so callers can build evidence states. */
export type { ProductState };
