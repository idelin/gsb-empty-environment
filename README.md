# gc-graph

C++ object graph with reference counting plus explicit cycle collection.

## Design

- `gcgraph::Object` — a node. `refCount()` counts **external roots naming it
  plus incoming internal edges**. Edges are raw non-owning pointers, so
  destruction never cascades through destructors.
- `gcgraph::Graph` — owns all objects and keeps the two kinds of references
  registered separately:
  - external roots: `addRoot(name, obj)` / `removeRoot(name)`
  - internal edges: `addEdge(from, to)` / `removeEdge(from, to)`
- `Graph::collect()` — mark & sweep from the external roots (iterative DFS).
  Anything unreachable, including mutually referencing cycles whose refcounts
  never drop to zero, is reclaimed. Sweep runs in two flat passes: dying
  objects first remove their outgoing edges (decrementing targets' counts),
  then a plain loop deletes them — no recursion, so million-node chains
  cannot overflow the stack.

## Tests

`tests/test_gc.cpp` uses plain BFS reachability over an independent model as
the judge. After every `collect()` the GC's live set and per-object refcounts
must match the oracle. Covered scenarios:

- removing a root reclaims a pure reference cycle
- shared subgraphs survive while any root/path remains (two roots, diamond)
- destroying an object removes its outgoing edges (dead cycle pointing into
  the live set drops its refcount contribution)
- 1,000,000-node chains collected and torn down without stack overflow
- 60k-operation randomized fuzz checked against the oracle

## Build & run

```sh
make test
```
