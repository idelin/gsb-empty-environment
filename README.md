# Network Fault Explainer (网络故障解释器)

Given a network topology and path probe observations, find the minimum sets of
faulty edges that explain every observation. A **failed** probe means at least
one edge on its path is faulty; a **successful** probe means every edge on its
path is healthy.

- Finds **all** minimum-cardinality fault sets (tied solutions are all listed).
- Supports at most **25 edges** in the topology.
- If the observations are mutually contradictory, reports the
  **minimum unsatisfiable observation subsets** (smallest cores).

## Requirements

Node.js >= 22.6 (runs TypeScript directly via type stripping; developed and
tested on Node 25). No dependencies to install.

## Usage

```sh
node src/main.ts examples/triangle.json
cat examples/contradiction.json | node src/main.ts
npm test
```

## Input format

```json
{
  "topology": {
    "edges": [["A", "B"], ["B", "C"], ["C", "A"]]
  },
  "observations": [
    { "id": "probe1", "path": ["A", "B", "C"], "result": "fail" },
    { "id": "probe2", "path": ["A", "B"], "result": "ok" }
  ]
}
```

- `topology.edges`: undirected edges as node-name pairs (max 25, no duplicates
  or self-loops).
- `observations[].path`: node sequence; every consecutive pair must be an edge
  in the topology.
- `observations[].result`: `"ok"` (all edges on the path are healthy) or
  `"fail"` (at least one edge on the path is faulty).

## Output format

Satisfiable input — every minimum fault set is listed:

```json
{
  "status": "ok",
  "minFaultEdges": 1,
  "solutionCount": 2,
  "solutions": [["e1"], ["e2"]],
  "edges": { "e0": ["A", "B"], "e1": ["B", "C"], "e2": ["C", "A"] }
}
```

Contradictory input — the smallest observation subsets that cannot be
satisfied together (a failed probe plus the successful probes whose paths
cover its entire path):

```json
{
  "status": "contradictory",
  "message": "observations are mutually contradictory: ...",
  "minUnsatisfiableCoreSize": 3,
  "coreCount": 1,
  "cores": [["bad", "ok1", "ok2"]]
}
```

Invalid input (malformed JSON, unknown edge in a path, more than 25 edges,
...) yields `{"status": "error", "message": "..."}` with exit code 1.

## How it works

1. Edges covered by any `ok` probe are forced healthy; the remaining edges are
   fault candidates.
2. Each `fail` probe becomes a hitting-set constraint: at least one of its
   candidate edges must be in the fault set. If a failed path has no candidate
   edges, the input is contradictory.
3. All minimum hitting sets are enumerated with a branch-and-bound DFS over
   edge bitmasks (smallest unhit constraint first, greedy upper bound,
   memoization on covered masks — 2^25 state space).
4. For contradictory inputs, each violated failed probe contributes cores of
   the form `{failed probe} ∪ {minimum set of ok probes covering its path}`;
   only the globally smallest cores are reported.

## Layout

- `src/solver.ts` — validation, hitting-set enumeration, unsat-core search
- `src/main.ts` — CLI (file argument or stdin, JSON in / JSON out)
- `test/solver.test.ts` — unit tests plus randomized brute-force cross-checks
- `examples/` — sample inputs
