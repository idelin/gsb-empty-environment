# regular-path-query

Regular path queries (RPQ) on a labeled directed graph: find paths whose
label word is accepted by a given finite automaton. TypeScript, zero
dependencies.

## Semantics

- The graph is directed with a finite label set; every edge has a unique
  numeric id. The automaton is an NFA (cycles allowed in both).
- Search states are product states `(graph node, automaton state)`. Each
  product state is visited at most once, so enumeration terminates even
  with cycles; each reachable accepting product state yields exactly one
  result — its best path.
- Results are ordered by **fewest edges first**, then by the **edge-id
  sequence compared lexicographically** (numeric ids). The empty path
  (length 0) is a result iff the initial automaton state is accepting.
- The same end node reached with different automaton states produces
  distinct results, since the product states differ.
- `disabledEdges` excludes edges from the search; `target` restricts the
  end node; `limit` returns the optimal prefix of the result order.

## Replay evidence

Every result carries `states`: the sequence of product states visited,
including the initial pair, with `states.length === edges.length + 1`.
`replayEvidence` re-checks a result step by step (edge membership,
disabled-edge use, automaton transitions, accepting final state), so the
evidence alone proves the match.

## Independent verification

`verifyQueryResults` cross-checks query output against a plain BFS over
the finite product graph (`independentProductBfs`), validating:

- every result replays cleanly and ends in a reachable accepting state;
- every result has minimal length (BFS distance) for its end state;
- result order and per-product-state uniqueness;
- completeness (all reachable accepting states appear, or an optimal
  prefix when `limit` is set);
- the named scenarios: empty path presence, same-node/different-
  automaton-state results, and confirmed unreachability.

## Layout

- `src/graph.ts` — labeled directed graph
- `src/automaton.ts` — NFA definition
- `src/query.ts` — the query engine (lexicographic BFS on product states)
- `src/verify.ts` — replay checker + independent product-graph BFS
- `test/run.ts` — test suite covering all scenarios above

## Running

Requires Node >= 23 (native type stripping of TypeScript), or compile
with `tsc` first:

```sh
node test/run.ts
```

## Example

```ts
import { Graph, Automaton, regularPathQuery, verifyQueryResults } from "./src/index.ts";

const graph = new Graph([
  { id: 1, from: "A", to: "B", label: "a" },
  { id: 2, from: "B", to: "B", label: "b" },
]);
const aut = new Automaton({
  states: ["q0", "q1"],
  initial: "q0",
  accepting: ["q1"],
  transitions: [
    { from: "q0", label: "a", to: "q1" },
    { from: "q1", label: "b", to: "q1" },
  ],
});

const results = regularPathQuery(graph, aut, "A", { disabledEdges: new Set() });
const report = verifyQueryResults(graph, aut, "A", {}, results);
console.assert(report.ok, report.errors);
```
