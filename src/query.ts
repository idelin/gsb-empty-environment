/**
 * Regular path query engine.
 *
 * The search runs on the product of the graph and the automaton: a
 * search state is the pair (graph node, automaton state). Both graph
 * and automaton may contain cycles; each product state is expanded at
 * most once, so the enumeration always terminates.
 *
 * Results are ordered by:
 *   1. number of edges (fewest first), then
 *   2. the edge-id sequence, compared lexicographically by numeric id.
 *
 * The first time a product state is generated in this order is via its
 * minimal path, so later paths reaching the same product state are
 * pruned. Distinct product states that share the graph node but differ
 * in the automaton state are separate results.
 */

import { Graph, type Edge, type EdgeId, type NodeId } from "./graph.ts";
import { Automaton, type AutomatonState } from "./automaton.ts";

export interface ProductState {
  readonly node: NodeId;
  readonly state: AutomatonState;
}

/** A matching path together with its state-replay evidence. */
export interface PathEvidence {
  /** Edges of the path, in traversal order. */
  readonly edges: readonly Edge[];
  /**
   * Product states visited, including the initial state.
   * `states.length === edges.length + 1`; `states[i]` is the pair
   * (node, automaton state) before edge `i`, and the last element is
   * the pair reached after the final edge. Replaying these states
   * against the graph and automaton proves the path matches.
   */
  readonly states: readonly ProductState[];
}

export interface QueryOptions {
  /** Edges that are treated as absent. */
  readonly disabledEdges?: ReadonlySet<EdgeId>;
  /** Maximum number of results to emit (default: unbounded). */
  readonly limit?: number;
  /** If set, only paths ending at this graph node are emitted. */
  readonly target?: NodeId;
}

export function productStateKey(node: NodeId, state: AutomatonState): string {
  return JSON.stringify([node, state]);
}

/** Compare two edge-id sequences lexicographically (numeric ids). */
export function compareEdgeIds(a: readonly EdgeId[], b: readonly EdgeId[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
  }
  return a.length - b.length;
}

/** Result order: fewest edges first, then edge-id sequence. */
export function compareEvidence(a: PathEvidence, b: PathEvidence): number {
  if (a.edges.length !== b.edges.length) return a.edges.length - b.edges.length;
  return compareEdgeIds(
    a.edges.map((e) => e.id),
    b.edges.map((e) => e.id),
  );
}

interface FrontierItem {
  readonly edges: readonly Edge[];
  readonly states: readonly ProductState[];
}

/**
 * Enumerate paths from `source` whose label word is accepted by
 * `automaton`, in (edge count, edge-id sequence) order.
 */
export function regularPathQuery(
  graph: Graph,
  automaton: Automaton,
  source: NodeId,
  options: QueryOptions = {},
): PathEvidence[] {
  const disabled = options.disabledEdges ?? new Set<EdgeId>();
  const limit = options.limit ?? Number.POSITIVE_INFINITY;
  const target = options.target;

  const results: PathEvidence[] = [];
  const start: ProductState = { node: source, state: automaton.initial };

  const emit = (item: FrontierItem): void => {
    const last = item.states[item.states.length - 1];
    if (!automaton.isAccepting(last.state)) return;
    if (target !== undefined && last.node !== target) return;
    results.push({ edges: item.edges, states: item.states });
  };

  // The empty path is the unique length-0 candidate.
  const seed: FrontierItem = { edges: [], states: [start] };
  emit(seed);

  const visited = new Set<string>([productStateKey(start.node, start.state)]);
  let frontier: FrontierItem[] = [seed];

  // Level-by-level BFS. Within a level, parents are already in
  // lexicographic edge-id order and children are generated in
  // ascending edge-id order, so `next` is again sorted: results are
  // emitted in exactly the required order.
  while (frontier.length > 0 && results.length < limit) {
    const next: FrontierItem[] = [];
    for (const item of frontier) {
      const last = item.states[item.states.length - 1];
      for (const edge of graph.outgoing(last.node)) {
        if (disabled.has(edge.id)) continue;
        for (const nextState of automaton.step(last.state, edge.label)) {
          const key = productStateKey(edge.to, nextState);
          if (visited.has(key)) continue;
          visited.add(key);
          const child: FrontierItem = {
            edges: [...item.edges, edge],
            states: [...item.states, { node: edge.to, state: nextState }],
          };
          next.push(child);
          emit(child);
        }
      }
    }
    frontier = next;
  }

  if (results.length > limit) results.length = limit;
  return results;
}
