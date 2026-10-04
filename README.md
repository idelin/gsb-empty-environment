# treeedit — ordered labeled tree edit distance

Computes the minimum-cost edit script between two ordered labeled trees with
insert / delete / rename operations, prints the operation sequence and the
source-to-target node mapping, and independently verifies every result.

## Semantics

- **rename** changes a node's label.
- **delete** removes a single node; its children are lifted into its place
  (order preserved). A delete is always charged the single-node cost of the
  node's label, never the size of its subtree.
- **insert** is the exact inverse of delete: a new node is placed among a
  parent's children and adopts the consecutive run of following siblings
  that belongs under it.

Costs come from a table of non-negative integers with per-label entries and
`*` defaults. The table need not be well-behaved: the solver first computes
its metric closure (inserting with a cheap label and renaming afterwards can
beat a direct insert, etc.), runs the Zhang–Shasha dynamic program on the
closed costs, and expands every logical operation back into the primitive
chain that realizes its cost. Zero-cost operations cannot make any search
loop: the DP backtrace strictly shrinks the subproblem each step, closure
chain reconstruction is length-bounded, and the exhaustive checker is a
Dijkstra search over a finite, capped state space.

## Usage

```sh
go build -o treeedit .
./treeedit -i example.json          # human-readable report
./treeedit -json -i example.json    # machine-readable report
cat example.json | ./treeedit       # stdin works too
```

Flags: `-brute auto|always|never` controls the exhaustive re-computation
(default `auto`: runs when the trees are small), `-max-states` caps its
state space.

Input format (see `example.json`): `costs` with `insert` / `delete` /
`rename` sections (`"A>B"` keys for renames, `*` for defaults), plus
`source` and `target` trees as nested `{"label": ..., "children": [...]}`.

Output: the distance, the node mapping (postorder ids), the edit script,
and the verification results. In scripts, node `#k` is the source node with
postorder id `k`, `#-k` is the node inserted for target postorder id `k`,
and `#0` is the virtual root above the forest.

## Verification

Every run checks itself twice:

1. **Independent applier** — re-applies the script to a fresh copy of the
   source, recomputes each operation's cost from the raw cost table
   (charging single-node deletes, never whole subtrees), and requires the
   result to equal the target and the total to equal the reported distance.
2. **Exhaustive search** — for short trees, a Dijkstra search over all
   reachable forests recomputes the distance from scratch and must agree
   with the dynamic program.

`go test` runs fixed examples plus randomized cross-checks of the dynamic
program, the generated script (via the independent applier) and the
exhaustive searcher, including zero-cost and cost-closure edge cases.
