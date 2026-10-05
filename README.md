# regular-path-query

Regular path queries (RPQ) on labeled directed graphs, in TypeScript.
Given a directed graph with a finite label set and an NFA over those labels,
the library finds paths whose edge-label sequence is accepted by the
automaton. Results are ordered by **fewest edges first**, ties broken by the
**lexicographic order of the edge id sequence**.

## Model

- The search state is always a **product state** `(graph node, automaton
  state)`. Both the graph and the automaton may contain cycles; termination
  is guaranteed because the product graph is finite and every product state
  is visited at most once per path.
- `Graph` is a directed multigraph with unique integer edge ids.
- `Automaton` is an NFA with explicit initial and accepting state sets.
- A set of **disabled edge ids** can be supplied per query; those edges are
  treated as absent.

## API

```ts
import { Graph, Automaton, findPath, enumeratePaths, verifyQueryResult } from "regular-path-query";

const graph = new Graph({
  nodeCount: 3,
  edges: [
    { id: 1, from: 0, to: 1, label: "a" },
    { id: 2, from: 1, to: 2, label: "b" },
  ],
});
const automaton = new Automaton({
  stateCount: 3,
  initialStates: [0],
  acceptingStates: [2],
  transitions: [
    { from: 0, label: "a", to: 1 },
    { from: 1, label: "b", to: 2 },
  ],
});

const disabled = new Set<number>([2]);
const result = findPath(graph, automaton, 0, disabled); // QueryResult | null
```

- `findPath(graph, automaton, startNode, disabledEdges?)` — the single best
  matching path, or `null` when unreachable.
- `enumeratePaths(graph, automaton, startNode, limit, disabledEdges?)` — up
  to `limit` matching paths in best-first order; each is a simple
  (cycle-free) path in the product graph.
- `verifyQueryResult(graph, automaton, startNode, result, disabledEdges?)` —
  independent verification, see below.

## Replay evidence

Every `QueryResult` carries a `PathEvidence` that fully determines the match
and can be replayed step by step:

- `states` — the product-state trajectory (`states.length === edgeIds.length + 1`);
- `edgeIds` — the edge id sequence;
- `steps` — per-step `{ edge, from, to }` triples.

The empty path is represented as a single state with an empty `edgeIds`
list (possible when an initial automaton state is also accepting).

## Independent verification

`verifyQueryResult` does not trust the search code:

1. `replayEvidence` re-checks the evidence against the raw graph and
   automaton: real non-disabled edges, matching endpoints and labels, valid
   automaton transitions, initial start and accepting end.
2. `verifyOptimality` rebuilds the **finite product graph** from scratch,
   runs a reverse BFS from all accepting product states to get exact minimum
   distances, then greedily reconstructs the lexicographically smallest
   shortest edge id sequence. The query result must match both — including
   the empty-path and unreachable cases.

The test suite additionally cross-checks `findPath` and `enumeratePaths`
against brute-force enumeration of all simple product-graph paths on
hundreds of random graphs/automata/disabled-edge combinations, covering
empty paths, same-node/different-automaton-state trajectories, and
unreachable queries.

## Build and test

```sh
npm run build   # tsc -p tsconfig.json
npm test        # build + run the test suite
```
