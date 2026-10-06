# tablegen

Go library that builds records matching group target counts while satisfying
two categorical marginal totals and forbidden (structural-zero) combinations.
Total records are capped at 100 (`MaxTotal`).

## API

- `Spec{RowTotals, ColTotals, Forbidden}` — marginals plus forbidden cells.
- `Solve(spec) (Table, error)` — returns a contingency table via max-flow,
  or an `*Evidence` Hall-style certificate (row subset whose demand exceeds
  the supply of its usable columns) when infeasible.
- `Generate(spec, rng) ([]Record, error)` — expands the table into one
  `Record` per count and shuffles with `rng`; shuffling permutes order only,
  never quotas. A nil `rng` keeps deterministic order.
- `Counts(recs, rows, cols) Table` — folds records back into a table.

## Verification

Tests check row/column conservation and structural zeros on random specs,
cross-validate feasibility against brute-force enumeration on small
instances, validate infeasibility certificates, and confirm shuffling
preserves per-cell quotas across seeds.
