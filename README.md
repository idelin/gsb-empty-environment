# bisim — coarsest bisimulation for labeled automata

Go library that partitions a finite automaton into the **coarsest
bisimulation** over output labels and full transition behavior, and emits
the quotient graph plus the old→new state mapping.

## Model

- States carry **output labels** (`Automaton.Outputs`).
- Transitions are **labeled multi-edges**: parallel edges are allowed and
  their multiplicity is semantically significant.
- Two states are bisimilar iff they have equal outputs and, for every edge
  label, equal *multisets* of successor blocks.

Consequences of the definition (enforced and tested):

- A **missing transition** is different from a transition into a rejecting
  state — both sides simply have different successor multisets.
- States are **never merged merely because they reach the same accepting
  states**; the partition observes the entire branching structure.

## API

- `automata.CoarsestBisimulation(a)` — signature-refinement fixed point,
  returns a `Partition` (`BlockOf`, `Blocks`, `Mapping()`).
- `automata.QuotientGraph(a, p)` — quotient multigraph (`QEdge` with
  per-representative multiplicities) plus block outputs.
- `automata.BisimulationFixedPoint(a)` — independent **direct relational
  model**: greatest fixed point of the counting-bisimulation operator on
  state pairs.
- `p.VerifyAgainstFixedPoint(a)` / `q.VerifyQuotient(a)` — cross-check the
  partition and quotient against the direct model.

## Run

```sh
go test ./automata/   # unit + randomized fixed-point agreement tests
go run ./cmd/bisim    # demo: prints blocks, mapping, quotient, checks
```
