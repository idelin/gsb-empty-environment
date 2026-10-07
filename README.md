# methodres

Go library for method resolution in a language where interfaces support
multiple inheritance and default methods, and classes may explicitly
override or select an ancestor's implementation.

## Resolution order

For a class `C` and method `m`:

1. **Class override** — `C.Overrides[m]` wins outright.
2. **Explicit selection** — `C.Selects[m]` names the ancestor interface
   whose default is used. It must be an *inheritable definition*: the
   interface has to be reachable from `C.Interfaces` and define a default
   for `m`; otherwise `Resolve` returns an error.
3. **Most specific inherited default** — among all inherited interfaces
   defining `m` (an ancestor reached via multiple paths is counted once),
   a definition at `A` dominates one at `B` when `B` is a proper ancestor
   of `A`. Exactly one undominated definition wins; two or more
   incomparable definitions are reported as `Conflict` with all
   `ConflictPair`s.

`Graph.Validate` rejects unknown parents and cyclic inheritance.

## Output

`Resolve` returns a `Resolution` with the verdict `Kind`, the `Winner`,
the conflict pairs, and `Sources` — the resolution source DAG: interfaces
lying on some inheritance path from a class root to a definition of the
method, with child→parent edges.

## Testing

`resolver_test.go` contains deterministic unit tests (diamond dedup,
specificity, conflicts, overrides, selection validation, cycle rejection)
plus a randomized cross-check: thousands of random small DAGs are resolved
both by the library and by an independent brute-force reference that
enumerates every inheritance path; verdicts, winners, conflict pairs, and
source DAGs must agree.

```sh
go test ./...
```
