# Subject-Level L1 Sensitivity Analyzer

Conservative, correlation-preserving L1 sensitivity analysis for a restricted
query expression language (sum, count, clipping, fixed grouping, subject-key
joins, sequential composition), with derivation trees and finite-domain
adjacency witnesses. Python 3.11, standard library + `unittest` only.
**Synthetic data only.**

## Definitions

- **Dataset**: named tables of rows; every row belongs to exactly one
  *subject* (the privacy unit). Row = `(subject, group, value)`, `value >= 0`.
- **Adjacency** (subject-level add/remove): `D ~ D'` iff one is obtained from
  the other by adding or removing **all** rows of a single subject.
- **L1 sensitivity**: `sens(q) = max_{D ~ D'} ||q(D) - q(D')||_1`.
- **Fixed grouping**: group-by keys are a fixed public set; empty groups
  report 0, so outputs are fixed-dimension vectors.
- **Declared contribution caps** (per table):
  - `m` — max rows per subject;
  - `b` — aggregated values lie in `[0, b]`.
  - Therefore a sum has sensitivity `<= m * b`; a count `<= m`.
  - **No declared cap => the query is rejected as UNBOUNDED.**

## Propagation rules (conservative, not operator-local max)

| Operator | Rows bound | Value bound | Query bound |
|---|---|---|---|
| `Table` | `m` (declared) | `b` (declared) | — |
| `Clip(hi)` | unchanged | `min(b, hi)` | — |
| `Join` (on subject id) | `m_L * m_R` (joint propagation) | carried side's `b` | — |
| `Aggregate sum` | — | — | `m * b` |
| `Aggregate count` | — | — | `m` |
| `GroupBy sum/count` (fixed groups) | — | — | `m * b` / `m` (L1 sums over groups; a subject's rows partition across groups) |
| `Compose` (sequential, same dataset) | — | — | **sum** of part bounds |

Key correlation facts the analyzer preserves:

- **Self-join / repeated reference**: a subject with `m` rows on each side of
  a join on the subject id produces up to `m * m` joined rows — not `m`, not
  `2m`. Taking an operator-local max would under-estimate.
- **Composition**: the *same* subject can attain every part's bound
  simultaneously, so sequential-composition bounds add and the sum is tight.
- **Group-by**: a subject's rows spread across groups; L1 is the *sum* of
  per-group deltas, which equals the total contribution — neither multiplied
  by the number of groups nor reduced to a per-group max.
- **Joins** require caps on *both* sides; an unbounded side rejects the query.

## Verification on a small finite domain

`enumerate_sensitivity` exhaustively enumerates all datasets over a small
synthetic row universe and all adjacent pairs `(D, D minus one subject)`:

- the largest observed L1 distance is a **lower bound** on the true
  sensitivity, returned as an adjacency **witness** `(D, D', subject)`;
- the check `observed <= derived bound` confirms the **upper bound** is never
  violated on the domain;
- for the demo queries the witness **attains** the bound, so the derived
  bound is tight there.

## Files

- `sensitivity.py` — AST, analyzer (derivation tree), evaluator, enumerator.
- `main.py` — runnable demo: 9 example queries with derivations + witnesses.
- `test_sensitivity.py` — 28 `unittest` tests (static bounds, evaluator,
  finite-domain verification).

## Run

```sh
python3.11 main.py                          # demo with derivation trees
python3.11 -m unittest test_sensitivity -v  # test suite
```

## Disclaimer

The finite-domain enumeration and any statistical experiment are **sanity
checks / witness generators, not formal privacy proofs**. The formal object
is the derivation tree: each bound follows from the declared per-table caps
and the propagation rules above. All data used here is synthetic.
