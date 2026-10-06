# pathtree

Tree with integer node loads supporting path operations. Paths are
undirected: the root passed to `NewTree` only affects the internal
heavy-light decomposition, never query results.

## API

- `NewTree(n int, edges [][2]int, root int, init []int64) *Tree` — build a
  tree on nodes `0..n-1`; `init` may be nil (all loads zero).
- `PathAdd(u, v int, delta int64)` — add `delta` (negative allowed) to every
  node on the u–v path.
- `PathMax(u, v int) (maxVal int64, node int)` — maximum load on the u–v
  path and the smallest-numbered node attaining it.

## Implementation

Heavy-light decomposition over a lazy-propagation segment tree. All tree
traversals are iterative, so arbitrarily deep (e.g. 300k-node chain) trees
do not depend on recursion stack depth. Updates and queries are
O(log^2 n).

## Tests

`go test ./...` runs: randomized cross-checks against a point-by-point
naive walker (including negative deltas), a single-node tree, a 300k-node
chain, and root-invariance checks across different roots.
