/**
 * Independent verification for regular path query results.
 *
 * Nothing here shares search logic with query.ts: the product graph
 * (graph nodes x automaton states) is finite, so a plain BFS over it
 * decides reachability of every accepting product state, and each
 * emitted path is re-checked by replaying its state sequence.
 */

import { Graph, type EdgeId, type NodeId } from "./graph.ts";
import { Automaton, type AutomatonState } from "./automaton.ts";
import {
  compareEvidence,
  productStateKey,
  type PathEvidence,
  type ProductState,
  type QueryOptions,
} from "./query.ts";

export interface ReachableAcceptingState {
  readonly node: NodeId;
  readonly state: AutomatonState;
  /** BFS distance (= minimum number of edges) from the start pair. */
  readonly distance: number;
}

export interface ProductBfsResult {
  /** Minimum edge count for every reachable product state. */
  readonly distances: ReadonlyMap<string, number>;
  /** All reachable product states whose automaton state is accepting. */
  readonly accepting: readonly ReachableAcceptingState[];
}

/**
 * Plain BFS over the finite product graph, honouring disabled edges.
 * Used as the ground truth the query results are checked against.
 */
export function independentProductBfs(
  graph: Graph,
  automaton: Automaton,
  source: NodeId,
  disabledEdges: ReadonlySet<EdgeId> = new Set(),
  target?: NodeId,
): ProductBfsResult {
  const distances = new Map<string, number>();
  const accepting: ReachableAcceptingState[] = [];
  const startKey = productStateKey(source, automaton.initial);
  distances.set(startKey, 0);
  let queue: ProductState[] = [{ node: source, state: automaton.initial }];

  const note = (node: NodeId, state: AutomatonState, distance: number): void => {
    if (automaton.isAccepting(state) && (target === undefined || node === target)) {
      accepting.push({ node, state, distance });
    }
  };
  note(source, automaton.initial, 0);

  while (queue.length > 0) {
    const next: ProductState[] = [];
    for (const cur of queue) {
      const d = distances.get(productStateKey(cur.node, cur.state))!;
      for (const edge of graph.outgoing(cur.node)) {
        if (disabledEdges.has(edge.id)) continue;
        for (const succ of automaton.step(cur.state, edge.label)) {
          const key = productStateKey(edge.to, succ);
          if (distances.has(key)) continue;
          distances.set(key, d + 1);
          note(edge.to, succ, d + 1);
          next.push({ node: edge.to, state: succ });
        }
      }
    }
    queue = next;
  }
  return { distances, accepting };
}

/**
 * Replay a path's state evidence step by step. Returns a list of
 * problems; an empty list means the evidence proves a valid match.
 */
export function replayEvidence(
  graph: Graph,
  automaton: Automaton,
  source: NodeId,
  evidence: PathEvidence,
  disabledEdges: ReadonlySet<EdgeId> = new Set(),
  target?: NodeId,
): string[] {
  const problems: string[] = [];
  const { edges, states } = evidence;

  if (states.length !== edges.length + 1) {
    problems.push(`states length ${states.length} != edges length ${edges.length} + 1`);
    return problems;
  }
  const first = states[0];
  if (first.node !== source || first.state !== automaton.initial) {
    problems.push(`does not start at (${source}, ${automaton.initial})`);
  }
  for (let i = 0; i < edges.length; i++) {
    const edge = edges[i];
    const before = states[i];
    const after = states[i + 1];
    if (graph.edge(edge.id) !== edge) {
      problems.push(`step ${i}: edge ${edge.id} not in graph`);
      continue;
    }
    if (disabledEdges.has(edge.id)) {
      problems.push(`step ${i}: edge ${edge.id} is disabled`);
    }
    if (edge.from !== before.node) {
      problems.push(`step ${i}: edge starts at ${edge.from}, state says ${before.node}`);
    }
    if (edge.to !== after.node) {
      problems.push(`step ${i}: edge ends at ${edge.to}, state says ${after.node}`);
    }
    if (!automaton.step(before.state, edge.label).includes(after.state)) {
      problems.push(
        `step ${i}: automaton has no ${before.state} --${edge.label}--> ${after.state} transition`,
      );
    }
  }
  const last = states[states.length - 1];
  if (!automaton.isAccepting(last.state)) {
    problems.push(`final automaton state ${last.state} is not accepting`);
  }
  if (target !== undefined && last.node !== target) {
    problems.push(`final node ${last.node} != target ${target}`);
  }
  return problems;
}

export interface VerificationReport {
  readonly ok: boolean;
  readonly errors: readonly string[];
  /** True iff the empty path is (and should be) among the results. */
  readonly emptyPath: { readonly expected: boolean; readonly found: boolean };
  /**
   * End nodes reached with more than one distinct automaton state
   * (same graph node, different product states, separate results).
   */
  readonly endNodesWithMultipleAutomatonStates: readonly NodeId[];
  /** True iff no accepting product state is reachable (and none emitted). */
  readonly confirmedUnreachable: boolean;
  /** Number of reachable accepting product states found by the independent BFS. */
  readonly reachableAcceptingStates: number;
}

/**
 * Cross-check query results against an independent product-graph BFS:
 * validity (replay), order, per-state minimality, and completeness.
 */
export function verifyQueryResults(
  graph: Graph,
  automaton: Automaton,
  source: NodeId,
  options: QueryOptions,
  results: readonly PathEvidence[],
): VerificationReport {
  const errors: string[] = [];
  const disabled = options.disabledEdges ?? new Set<EdgeId>();
  const limit = options.limit ?? Number.POSITIVE_INFINITY;

  // 1. Replay every piece of evidence independently.
  results.forEach((r, i) => {
    for (const p of replayEvidence(graph, automaton, source, r, disabled, options.target)) {
      errors.push(`result ${i}: ${p}`);
    }
  });

  // 2. Order: (edge count, edge-id sequence), ties allowed only between
  //    results that end in different product states.
  for (let i = 1; i < results.length; i++) {
    if (compareEvidence(results[i - 1], results[i]) > 0) {
      errors.push(`results ${i - 1} and ${i} are out of order`);
    }
  }

  // 3. At most one result per product state.
  const endKeys = new Set<string>();
  results.forEach((r, i) => {
    const last = r.states[r.states.length - 1];
    const key = productStateKey(last.node, last.state);
    if (endKeys.has(key)) errors.push(`result ${i}: duplicate end product state ${key}`);
    endKeys.add(key);
  });

  // 4. Independent ground truth from the finite product graph.
  const truth = independentProductBfs(graph, automaton, source, disabled, options.target);
  const truthKeys = new Set(
    truth.accepting.map((a) => productStateKey(a.node, a.state)),
  );

  // 4a. Every emitted path reaches a genuinely reachable accepting
  //     state, via a path of minimal length.
  results.forEach((r, i) => {
    const last = r.states[r.states.length - 1];
    const key = productStateKey(last.node, last.state);
    const dist = truth.distances.get(key);
    if (dist === undefined) {
      errors.push(`result ${i}: end state ${key} is not reachable`);
    } else if (dist !== r.edges.length) {
      errors.push(`result ${i}: length ${r.edges.length} is not minimal (BFS distance ${dist})`);
    }
  });

  // 4b. Completeness: without a limit, every reachable accepting
  //     product state must appear; with a limit, exactly
  //     min(limit, count) results must appear.
  for (const key of truthKeys) {
    if (!endKeys.has(key)) {
      if (results.length < limit) {
        errors.push(`reachable accepting state ${key} missing from results`);
      }
    }
  }
  // With a limit, the emitted prefix must be optimal: no missing
  // reachable accepting state may be strictly closer than the last
  // emitted result.
  if (results.length === limit && results.length > 0) {
    const maxEmittedLength = results[results.length - 1].edges.length;
    for (const a of truth.accepting) {
      const key = productStateKey(a.node, a.state);
      if (!endKeys.has(key) && a.distance < maxEmittedLength) {
        errors.push(
          `limited results are not an optimal prefix: missing ${key} at distance ${a.distance}`,
        );
      }
    }
  }
  const expectedCount = Math.min(limit, truth.accepting.length);
  if (results.length !== expectedCount) {
    errors.push(`expected ${expectedCount} results, got ${results.length}`);
  }

  // Named scenario observations.
  const emptyExpected =
    automaton.isAccepting(automaton.initial) &&
    (options.target === undefined || options.target === source) &&
    limit > 0;
  const emptyFound = results.some((r) => r.edges.length === 0);
  if (emptyExpected !== emptyFound) {
    errors.push(
      `empty path: expected ${emptyExpected}, found ${emptyFound}`,
    );
  }

  const statesByNode = new Map<NodeId, Set<AutomatonState>>();
  for (const r of results) {
    const last = r.states[r.states.length - 1];
    let set = statesByNode.get(last.node);
    if (set === undefined) {
      set = new Set();
      statesByNode.set(last.node, set);
    }
    set.add(last.state);
  }
  const multi: NodeId[] = [];
  for (const [node, set] of statesByNode) {
    if (set.size > 1) multi.push(node);
  }
  multi.sort();

  return {
    ok: errors.length === 0,
    errors,
    emptyPath: { expected: emptyExpected, found: emptyFound },
    endNodesWithMultipleAutomatonStates: multi,
    confirmedUnreachable: results.length === 0 && truth.accepting.length === 0,
    reachableAcceptingStates: truth.accepting.length,
  };
}
