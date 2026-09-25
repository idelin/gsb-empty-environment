# typedpred

Typed predicate trees, three-valued logic, and block-level statistics
pruning in pure Python 3.11 (standard library only, no query engines).

## Design

- `typedpred/tvl.py` — three-valued logic `TRUE / FALSE / UNKNOWN`.
  `NOT UNKNOWN = UNKNOWN`; `FALSE AND UNKNOWN = FALSE`;
  `TRUE OR UNKNOWN = TRUE`.
- `typedpred/types.py` — column types (`INT`, `FLOAT`, `STRING`, `BOOL`),
  schema, and type checking. Integers are arbitrary precision and compared
  exactly; `INT`/`FLOAT` are mutually comparable, everything else must match.
- `typedpred/batch.py` — typed columnar `Batch` plus `ColumnStats`.
  `min`/`max` are computed **only over valid values** (non-null, non-NaN);
  `null_count` and `nan_count` are tracked separately. NaN can therefore
  never contaminate statistics and "prove" a block empty.
- `typedpred/predicate.py` — typed predicate tree (`Col`, `Lit`, `Compare`,
  `IsNull`, `IsNotNull`, `And`, `Or`, `Not`) with two evaluators:
  - `eval_row` — per-row TVL interpreter. Null in a comparison is UNKNOWN;
    NaN follows IEEE-754 (`NaN != c` is TRUE, everything else FALSE);
    `IS NULL` is FALSE for NaN (null and NaN are distinct).
  - `eval_block` — maps predicate + stats to a TVL verdict where
    FALSE means "provably no row can be TRUE". Only FALSE permits skipping.
- `typedpred/engine.py` — `prune_decision` (skip + human-readable reason),
  `row_scan` (independent interpreter), `select`, and `cross_check`, which
  runs both and raises if a skip ever hides a TRUE row.

## Semantics highlights

- All-null block: `x > 2` is skippable (null -> UNKNOWN, never TRUE);
  `x IS NULL` is not skippable.
- A block containing NaN never yields a wrong "empty" proof: NaN is excluded
  from `min`/`max`, counted separately, and satisfies `!=` per IEEE-754.
- `NOT UNKNOWN` stays UNKNOWN, so `NOT(x > 2)` on a block containing nulls
  must scan, and the null row is excluded from results.

## Run

    python3.11 demo.py            # worked examples with skip reasons
    python3.11 -m unittest -v     # 24 tests incl. randomized soundness check
