# certcache

Certificate-based verification cache for graph queries (Go).

## Model

- Every edge carries a monotonically increasing **version**; the per-edge
  counter survives deletion, so re-added edges never recycle a version.
- Versions are bounded (`NewGraph(maxVersion)`); a mutation that would
  exceed the bound is rejected with `ErrVersionWraparound`, so a stale
  certificate can never match a wrapped-around version number.
- **Certificates** record their witness path plus the exact dependency
  edge versions, and are managed in **separate stores per type**
  (reachability vs. shortest-path).

## Invalidation rules

| Mutation              | Reachability certs        | Shortest-path certs            |
|-----------------------|---------------------------|--------------------------------|
| Edge removed          | only dependents of edge   | only dependents of edge        |
| Weight increased      | only dependents of edge   | only dependents of edge        |
| Weight decreased      | only dependents of edge   | all (acts like an insertion)   |
| Edge inserted         | unaffected                | all (a shorter path may exist) |

Shortest-path certificates additionally record the graph's add epoch;
`Validate` rejects them once any insertion (or weight decrease) has
bumped the epoch.

## Cross-checking

`FullReachable` / `FullShortest` recompute answers from scratch (BFS /
Dijkstra). `TestFullReverificationCrossCheck` replays a mutation script
and, after every step, compares cache answers against full
re-verification for all node pairs and confirms surviving certificates
were kept by identity (targeted, not wholesale, invalidation).

## Test

```sh
go test -race ./...
```
