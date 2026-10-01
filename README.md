# alldiff — Régin's AllDifferent Filtering Algorithm

Go implementation (standard library only) of Régin's propagator for the
AllDifferent global constraint, from *"A Filtering Algorithm for Constraints
of Difference in CSPs"* (AAAI-94).

## How it works

1. **Bipartite model** — variables on the left, the union of their domains
   on the right; edge `(Xi, v)` iff `v ∈ domain(Xi)`.
2. **Maximum matching (Hopcroft–Karp)** — if no matching saturates all
   variables, the constraint is infeasible and reported *before any search*
   (Hall's theorem).
3. **SCC filtering (Tarjan)** — matched edges are oriented value→variable,
   unmatched edges variable→value, and a dummy node closes alternating
   paths starting at free values. An edge survives iff it is matched or its
   endpoints lie in the same strongly connected component; every other edge
   belongs to no maximum matching and is pruned from the domain.

## Layout

- `alldiff.go` — the propagator (`Propagate`), Hopcroft–Karp, Tarjan SCC.
- `alldiff_test.go` — `go test` suite, including the acceptance boundary.
- `cmd/demo/main.go` — runnable demonstration.

## Run

```sh
go test -v ./...
go run ./cmd/demo
```

## Acceptance boundary

`X1..X4` all with domain `{1, 2, 3}`: four variables cannot take distinct
values from a three-value pool. The matching phase covers only 3 of the 4
variables, so `Propagate` reports infeasibility instantly — no search, no
combinatorial blow-up:

```
=> INFEASIBLE (detected at matching stage, before any search)
   reason: no matching saturates all 4 variables: maximum matching
   covers 3 variables over 3 distinct values (Hall set violated)
```

## Test results

```
=== RUN   TestAcceptanceFourVariablesThreeValues  --- PASS
=== RUN   TestInfeasibleSingletonClash            --- PASS
=== RUN   TestSCCFilteringPrunesHallSetEdges      --- PASS
=== RUN   TestFullyInterchangeableDomainsKeepAllEdges --- PASS
=== RUN   TestFilteringIsIdempotent               --- PASS
=== RUN   TestMatchingIsSystemOfDistinctRepresentatives --- PASS
PASS
ok      alldiff
```
