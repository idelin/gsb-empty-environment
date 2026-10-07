# bisim

Go library computing the **coarsest bisimulation partition** of a finite
automaton with state output labels and labeled multi-edges, plus the
quotient graph and the old-state → new-block mapping.

## Semantics

States `s` and `t` are bisimilar iff:

1. `output(s) == output(t)`, and
2. for every edge `s -l-> s'` there is an edge `t -l-> t'` with `s'`, `t'`
   bisimilar, and symmetrically.

Consequences the library guarantees:

- A **missing transition** is different from a transition into a
  rejecting state (a state with no `l`-move can never match one with an
  `l`-move).
- States are **never merged merely for being reachable and accepting** —
  the full move structure must match.
- **Multi-edges** (duplicate/parallel labeled edges) are supported; per
  standard bisimulation they do not affect the partition.

## API

- `New(n, outputs)` / `AddEdge(from, label, to)` — build the automaton.
- `BisimulationQuotient() *Quotient` — partition refinement; returns
  `BlockOf` (old → new mapping), per-block `Output`, and quotient `Edges`.
- `BisimulationRelation() [][]bool` — naive direct fixed-point relation
  model (independent cross-check).
- `CheckQuotient(q) error` — verifies a quotient against the fixed-point
  relation and the concrete edge projection.

## Test

```sh
go test ./...
```

Includes a randomized cross-check of the refinement algorithm against
the fixed-point relation model (500 random automata with multi-edges).
