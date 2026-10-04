# mincutenum

Enumerate all minimum s-t cuts of a directed, non-negative integer
capacitated network: compute the max flow, then list every minimum cut
node set (the s-side) in lexicographic order, page by page.

## Build & test

```sh
go build -o mincutenum .
go test ./...
```

## Input format

1-based vertices, non-negative integer capacities (read from stdin or `-i file`):

```
n m s t
u1 v1 c1
...
um vm cm
```

## Usage

```sh
mincutenum -i graph.txt -limit 100
mincutenum -i graph.txt -limit 100 -cursor 'mce1....'
```

- `-limit N` — page size (max cuts per page, default 100; `0` = unlimited).
- `-cursor S` — resume strictly after the given cursor.
- `-selfcheck-max N` — when `n <= N` (default 20), independently recompute
  every one of the `2^(n-2)` subsets and cross-check the max-flow value and
  the full cut list; `-1` disables.

## Output (JSON)

- `max_flow` — max-flow value (Dinic, int64).
- `cuts` — one page of minimum cuts, each an ascending 1-based vertex list.
  Pages follow lexicographic order on these lists (a prefix sorts before
  its extensions).
- `complete` — true only when every minimum cut has been listed. If the
  page budget is exhausted first, `complete` is false and `next_cursor`
  resumes the listing; the tool never claims completeness on a budget stop.
- `next_cursor` — opaque token bound to `graph_version` (a SHA-256
  fingerprint of the graph). Reusing it after any change to the graph is
  rejected with an error.
- `verified` — every emitted cut was independently re-checked to have
  capacity (summed over original arcs leaving the set) equal to `max_flow`.
- `self_check` — present for small graphs: result of the exhaustive
  `2^(n-2)` subset recomputation.

Exit codes: `0` ok, `1` bad input/usage, `2` internal verification failure.

## How enumeration works

By Picard-Queyranne, the minimum s-t cuts are exactly the vertex sets that
contain s, exclude t, and are closed under reachability in the residual
graph of a max flow. Many different residual paths lead to the same closed
set, so the enumerator never follows residual paths directly. Instead it
walks the trie of ascending vertex lists: a node is a prefix P, a child
extends P with the next vertex b > max(P), and a prefix is feasible iff
some closed set has exactly that prefix. Each cut has exactly one ascending
list, hence is emitted exactly once — no duplicates from converging
residual paths — and emission order is lexicographic.

Resuming from a cursor re-enumerates from the start and skips cuts up to
the cursor position, so a resume costs time linear in the number of cuts
already listed.
