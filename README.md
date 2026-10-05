# mincut — minimum s-t cut enumerator

`mincut` reads a directed network with non-negative integer capacities,
computes the maximum s-t flow, and lists the minimum s-t cuts (as
source-side node sets) in lexicographic order, page by page.

## Usage

```
mincut -graph graph.json [-page-size N] [-cursor C] [-budget B] [-brute-threshold K]
```

- `-graph`: input JSON file, or `-` for stdin (default `-`).
- `-page-size`: cuts per page (default 50).
- `-cursor`: opaque token from a previous response's `next_cursor`.
- `-budget`: maximum number of closed sets generated during enumeration
  (default 100000, `-1` for unlimited).
- `-brute-threshold`: exhaustively recompute all 2^(n-2) cuts for graphs
  up to this many nodes and reconcile (default 20, `0` disables).

## Input

```json
{
  "version": "demo-v1",
  "source": "s",
  "sink": "t",
  "nodes": ["x"],
  "edges": [{"from": "s", "to": "a", "capacity": 3}]
}
```

Parallel edges are allowed (capacities add up); self-loops never cross a
cut. `version` is optional: when omitted, a deterministic content hash of
the canonicalized network is used.

## Output

```json
{
  "graph_version": "demo-v1",
  "max_flow": 2,
  "offset": 0,
  "page_size": 50,
  "known_total": 8,
  "complete": true,
  "next_cursor": "eyJ2Ijoi...",
  "cuts": [{"nodes": ["a", "s"], "capacity": 2}],
  "verification": {"per_cut_capacity": "ok", "brute_force": "ok"}
}
```

## Semantics

- **Enumeration.** Minimum s-t cuts correspond one-to-one to the closed
  sets of the residual strongly-connected-component DAG of any maximum
  flow (Picard–Queyranne). Enumerating closed sets on the component
  quotient collapses the different residual-path representations of one
  and the same cut; results are additionally deduplicated by their
  canonical node-set key.
- **Ordering.** Each cut's node names are sorted ascending; cuts are
  ordered lexicographically by those sequences (shorter prefix first).
- **Cursor.** The cursor binds to `graph_version`; a cursor presented
  against a different graph version is rejected.
- **Budget.** If the enumeration budget is exhausted, `complete` is
  `false` and the tool never claims the listing is exhaustive; pages
  already emitted remain valid minimum cuts.
- **Verification.** Every emitted cut's capacity is recomputed
  independently from the original edges and checked against the maximum
  flow. For small graphs all subsets are enumerated exhaustively and the
  full cut set is reconciled. Exit code is `2` if any verification fails,
  `1` for input/cursor errors.

## Development

```
go test ./...
```
