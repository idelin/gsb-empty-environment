# rule-tree-match

Deterministic pattern matching over rule trees, in TypeScript (no runtime
dependencies; runs directly on Node >= 23 via type stripping).

## Trees and patterns

A **tree** is made of literal atoms (`string | number | boolean | null`) and
lists (arrays of trees). A **pattern** additionally supports:

- `v("x")` — single-node variable: matches exactly one node (atom or list).
- `t("rest")` — tail variable: matches the (possibly empty) rest of a list and
  may only appear as the **last element of a list**. `validatePattern` /
  `match` throw `PatternError` otherwise.

A variable with the same name must bind to a **structurally equal** value at
every occurrence, otherwise the match fails with `variable-conflict`.

## API

```ts
import { match, substitute, equalTree, v, t } from "./src/match.ts";

match([v("x"), ["a", t("rest")], v("x")], [[1], ["a", 2, 3], [1]]);
// => { ok: true, bindings: [["x", [1]], ["rest", [2, 3]]] }

match([v("x"), v("x")], ["a", "b"]);
// => { ok: false, failure: { path: [1], reason: "variable-conflict", ... } }
```

- `match(pattern, tree)` — on success returns **stable bindings**: each name
  once, in first-occurrence pre-order, identical across runs. On failure
  returns the **shallowest failure path** (fewest indices from the root, ties
  broken leftmost) with a reason: `atom-mismatch`, `kind-mismatch`,
  `length-mismatch`, or `variable-conflict`.
- `substitute(pattern, bindings)` — instantiates a pattern with bindings;
  tail variables splice their list value into the surrounding list.
- `equalTree(a, b)` — structural equality on trees.
- `patternVariables(pattern)` — lists the single/tail variable names used.

## Verification

`npm test` runs:

- `test/match.test.ts` — unit tests: repeated variables, nested tails, empty
  tails, tail-placement validation, shallowest-failure selection, binding
  stability, substitution round-trips.
- `test/exhaustive.test.ts` — exhaustive small-tree verification: all trees
  up to depth 2 / list length 2 over two atoms (93 trees) are matched against
  all generated patterns (up to ~1.7k per suite) covering repeated single
  variables and nested tail variables. Every pair is checked against a
  brute-force oracle (does *any* assignment make the pattern equal the tree
  after substitution?), plus substitution-equality on success and
  failure-path validity/shallowness on failure.
