# methodres

Go library for method resolution in a language where interfaces multiply
inherit and define default methods, and classes override methods or
explicitly pick an ancestor's implementation.

## Semantics

For a `(class, method)` pair, resolution proceeds in order:

1. **Class override** — a method defined on the class itself wins.
2. **Explicit pick** — `Class.Picks[m]` names an ancestor interface whose
   default is selected. Validated at graph construction: the target must be
   an interface, reachable through inheritance, and actually define a
   default for the method.
3. **Most specific inherited default** — among all interfaces reachable
   from the class that define a default, the most specific one wins.
   Interface A is *more specific* than B iff B is an ancestor of A.
   - The same ancestor reached via multiple paths (diamond) counts once.
   - Two incomparable most-specific defaults are a **conflict**; all
     conflict pairs are reported.

Cyclic interface inheritance is rejected by `NewGraph`.

## API

- `NewGraph(ifaces, classes)` — validate and build a program graph.
- `NewResolver(g).Resolve(class, method)` — resolve one method; returns a
  `Resolution` with `Kind` (`ClassOverride` / `ExplicitPick` / `Inherited` /
  `Conflict` / `None`), the defining entity `From`, and conflict `Pairs`.
- `NewResolver(g).SourceDAG(class, methods)` — adjudication-source DAG:
  reachable nodes, inheritance edges, one `resolves` edge per settled
  method, and per-method conflict pairs.
- `ReferenceResolve(g, class, method)` — naive path-enumeration oracle used
  to cross-check the memoized resolver in tests.

## Tests

`go test ./...` runs semantic unit tests plus a randomized comparison of
the memoized resolver against the path-enumeration reference over 2000
random small DAGs.
