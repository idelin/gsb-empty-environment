import { Automaton } from "./automaton";
import { Graph } from "./graph";
import {
  Edge,
  EdgeId,
  NodeId,
  ProductState,
  compareEdgeIdSequences,
  productStateKey,
} from "./types";

/** One replayable step: a concrete edge plus the product states it connects. */
export interface PathStep {
  edge: Edge;
  from: ProductState;
  to: ProductState;
}

/**
 * Full replay evidence for a matching path: the product-state trajectory and
 * the edge id sequence. `states.length === edgeIds.length + 1` always holds.
 */
export interface PathEvidence {
  startNode: NodeId;
  states: ProductState[];
  edgeIds: EdgeId[];
  steps: PathStep[];
}

export interface QueryResult {
  evidence: PathEvidence;
  edgeCount: number;
}

interface FrontierItem {
  state: ProductState;
  edgeIds: EdgeId[];
  steps: PathStep[];
}

function toEvidence(startNode: NodeId, item: FrontierItem, initial: ProductState): PathEvidence {
  const states: ProductState[] = [initial];
  for (const step of item.steps) states.push(step.to);
  return { startNode, states, edgeIds: item.edgeIds.slice(), steps: item.steps.slice() };
}

function checkStartNode(graph: Graph, startNode: NodeId): void {
  if (!Number.isInteger(startNode) || startNode < 0 || startNode >= graph.nodeCount) {
    throw new Error("start node out of range: " + startNode);
  }
}

/**
 * Breadth-first enumeration of matching paths over the finite product graph
 * (graph node x automaton state). Results are produced in order of fewest
 * edges first, ties broken by lexicographic order of the edge id sequence.
 *
 * Both the graph and the automaton may contain cycles; termination is
 * guaranteed because every product state is expanded at most once, so only
 * cycle-free (in product space) paths are produced.
 *
 * The `onResult` callback may return false to stop early.
 */
function search(
  graph: Graph,
  automaton: Automaton,
  startNode: NodeId,
  disabledEdges: ReadonlySet<EdgeId>,
  onResult: (result: QueryResult) => boolean | void
): void {
  checkStartNode(graph, startNode);
  const visited = new Set<string>();
  let frontier: FrontierItem[] = [];

  for (const q0 of automaton.initialStates()) {
    const initial: ProductState = { node: startNode, automatonState: q0 };
    const key = productStateKey(initial);
    if (visited.has(key)) continue;
    visited.add(key);
    const item: FrontierItem = { state: initial, edgeIds: [], steps: [] };
    if (automaton.isAccepting(q0)) {
      // Empty path: the start node already matches.
      if (onResult({ evidence: toEvidence(startNode, item, initial), edgeCount: 0 }) === false) {
        return;
      }
    }
    frontier.push(item);
  }

  while (frontier.length > 0) {
    const nextFrontier: FrontierItem[] = [];
    // Frontier items are stored in lexicographic order of their edge id
    // sequences, and outgoing edges are scanned in ascending id order, so
    // children are generated in lexicographic order as well; the first time
    // a product state is generated is on its best (shortest, then smallest
    // edge id sequence) path.
    for (const item of frontier) {
      for (const edge of graph.outgoingFrom(item.state.node, disabledEdges)) {
        for (const q2 of automaton.next(item.state.automatonState, edge.label)) {
          const to: ProductState = { node: edge.to, automatonState: q2 };
          const key = productStateKey(to);
          if (visited.has(key)) continue;
          visited.add(key);
          const step: PathStep = { edge, from: item.state, to };
          const child: FrontierItem = {
            state: to,
            edgeIds: item.edgeIds.concat(edge.id),
            steps: item.steps.concat(step),
          };
          if (automaton.isAccepting(q2)) {
            const initial: ProductState =
              child.steps.length > 0 ? child.steps[0].from : to;
            if (
              onResult({
                evidence: toEvidence(startNode, child, initial),
                edgeCount: child.edgeIds.length,
              }) === false
            ) {
              return;
            }
          }
          nextFrontier.push(child);
        }
      }
    }
    frontier = nextFrontier;
  }
}

/**
 * Finds the best path from `startNode` whose edge label sequence is accepted
 * by `automaton`: fewest edges first, then lexicographically smallest edge id
 * sequence. Returns null when no matching path exists.
 */
export function findPath(
  graph: Graph,
  automaton: Automaton,
  startNode: NodeId,
  disabledEdges?: ReadonlySet<EdgeId>
): QueryResult | null {
  let best: QueryResult | null = null;
  search(graph, automaton, startNode, disabledEdges ?? new Set<EdgeId>(), (result) => {
    best = result;
    return false;
  });
  return best;
}

interface Candidate {
  state: ProductState;
  edgeIds: EdgeId[];
  steps: PathStep[];
  visited: Set<string>;
}

/** Min-heap of candidates ordered by (edge count, edge id sequence). */
class CandidateHeap {
  private readonly items: Candidate[] = [];

  get size(): number {
    return this.items.length;
  }

  push(item: Candidate): void {
    const items = this.items;
    items.push(item);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (compareEdgeIdSequences(items[parent].edgeIds, items[i].edgeIds) <= 0) break;
      const tmp = items[parent];
      items[parent] = items[i];
      items[i] = tmp;
      i = parent;
    }
  }

  pop(): Candidate {
    const items = this.items;
    const top = items[0];
    const last = items.pop() as Candidate;
    if (items.length > 0) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < items.length &&
            compareEdgeIdSequences(items[left].edgeIds, items[smallest].edgeIds) < 0) {
          smallest = left;
        }
        if (right < items.length &&
            compareEdgeIdSequences(items[right].edgeIds, items[smallest].edgeIds) < 0) {
          smallest = right;
        }
        if (smallest === i) break;
        const tmp = items[smallest];
        items[smallest] = items[i];
        items[i] = tmp;
        i = smallest;
      }
    }
    return top;
  }
}

/**
 * Enumerates up to `limit` matching paths in best-first order (fewest edges,
 * then lexicographic edge id sequence). Unlike findPath, every simple path in
 * the product graph (graph node x automaton state, no repeated product state
 * along one path) is a separate candidate, so several paths may share the
 * same product states. Termination is guaranteed because the product graph
 * is finite and each enumerated path is cycle-free in product space.
 */
export function enumeratePaths(
  graph: Graph,
  automaton: Automaton,
  startNode: NodeId,
  limit: number,
  disabledEdges?: ReadonlySet<EdgeId>
): QueryResult[] {
  checkStartNode(graph, startNode);
  if (!Number.isInteger(limit) || limit < 0) {
    throw new Error("limit must be a non-negative integer");
  }
  const disabled = disabledEdges ?? new Set<EdgeId>();
  const heap = new CandidateHeap();
  for (const q0 of automaton.initialStates()) {
    const state: ProductState = { node: startNode, automatonState: q0 };
    heap.push({
      state,
      edgeIds: [],
      steps: [],
      visited: new Set<string>([productStateKey(state)]),
    });
  }
  const results: QueryResult[] = [];
  while (heap.size > 0 && results.length < limit) {
    const candidate = heap.pop();
    if (automaton.isAccepting(candidate.state.automatonState)) {
      const initial: ProductState =
        candidate.steps.length > 0 ? candidate.steps[0].from : candidate.state;
      results.push({
        evidence: toEvidence(startNode, candidate, initial),
        edgeCount: candidate.edgeIds.length,
      });
      if (results.length >= limit) break;
    }
    for (const edge of graph.outgoingFrom(candidate.state.node, disabled)) {
      for (const q2 of automaton.next(candidate.state.automatonState, edge.label)) {
        const to: ProductState = { node: edge.to, automatonState: q2 };
        const key = productStateKey(to);
        if (candidate.visited.has(key)) continue;
        const visited = new Set<string>(candidate.visited);
        visited.add(key);
        heap.push({
          state: to,
          edgeIds: candidate.edgeIds.concat(edge.id),
          steps: candidate.steps.concat({ edge, from: candidate.state, to }),
          visited,
        });
      }
    }
  }
  return results;
}
