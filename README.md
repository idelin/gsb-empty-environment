# Synthetic-Only Differentially Private Query Engine

Python 3.11, standard library only. A small engine that answers scalar
`count` / bounded `sum` queries over **synthetic** data with the Laplace
mechanism, and keeps an epsilon ledger with a canonical, version-aware release
cache and concurrent-query de-duplication.

## Parameters and Definitions

- **Adjacency (neighboring datasets).** Datasets `D, D'` differ by adding or
  removing one individual's single record (`add/remove` adjacency).
- **Mechanism.** Laplace with scale `b = Δf / ε`:
  `M(x) = f(x) + Z`, `Z ~ Laplace(0, b)`. Pure **ε-differential privacy**,
  `δ = 0`. (`delta` is deliberately not supported.)
- **Global sensitivity `Δf`.**
  - `count` after a conjunction of row-level filters: `Δ = 1`.
  - `sum(column)` where each record contribution is clipped to `[lo, hi]`:
    `Δ = max(|lo|, |hi|)`. Clip bounds are therefore mandatory and are part
    of the query specification.
- **Sequential composition.** A fresh release charged `ε_i` consumes budget;
  total cost over the ledger is `Σ ε_i ≤ ε_total`.
- **Data version.** Each synthetic dataset carries an explicit `version` tag
  derived deterministically from generator seed, row count and generator
  revision (e.g. `synthetic-seed20260927-n400-v1`). Mutating/regenerating data
  produces a new version.
- **Canonical query spec (`Query.canonical_spec`).** Deterministic JSON of
  `{kind, column, sorted predicate tokens, bounds}`. Predicate (field)
  construction order is irrelevant; thresholds, operators, the target column
  and clip bounds are all part of the spec.
- **Cache key.** `{mechanism, epsilon, dataset.name, dataset.version, spec}`
  serialized deterministically. Any change to query semantics (including a
  threshold), data version, mechanism, or epsilon changes the key.

## Budget and Reuse Rules

1. Exact same canonical spec **and** same data version: reuse the published
   noisy value; spend **0** additional epsilon (`reused=True`).
2. Changed query semantics or data version: cache miss; reserve and charge
   epsilon again and draw fresh noise. A changed threshold or clip bound can
   never reuse an old release.
3. Different epsilon (even with the same query/data) is a distinct release and
   is charged separately.
4. Concurrent identical requests share one cache key: exactly one leader
   reserves budget and draws one mechanism output; followers wait and receive
   the same value, and budget is charged exactly once.
5. Budget reservation is atomic: if the leader fails before publishing (e.g. a
   bad column), the reservation is refunded and all followers receive the
   error. No output, no charge.
6. When the budget is exhausted, new keys are rejected with
   `BudgetExhaustedError`; already published keys remain readable for free.

## Scope and Disclaimer

- **Synthetic data only.** `PrivateQueryEngine` type-checks for
  `SyntheticDataset` (tagged `kind="synthetic"`); the generator is seeded and
  deterministic. Do not point this at real records.
- The unit-test checks of mean/variance of Laplace samples are **statistical
  sanity experiments**, not a formal privacy proof. The privacy claim rests on
  the mathematical guarantee of the Laplace mechanism under the stated
  add/remove adjacency and the fixed sensitivities above.

## Layout

- `dpquery/models.py` — `Predicate`, `Query` (canonical spec), `SyntheticDataset`
- `dpquery/accountant.py` — epsilon ledger (`BudgetExhaustedError`)
- `dpquery/mechanisms.py` — `LaplaceMechanism` (seeded RNG, inverse-CDF)
- `dpquery/engine.py` — cache + in-flight de-duplication + atomic refund
- `dpquery/synthetic.py` — deterministic synthetic data generators
- `demo.py` — runnable sample
- `tests/test_dpquery.py` — 17 unittest cases

## Run Sample

```
$ python3.11 demo.py
dataset=synthetic_users version=synthetic-seed20260927-n400-v1 records=400
--- fresh release (charged 0.5) ---
  count(age>=18 & city=Beijing)              value=     92.34 reused=False spent=0.50
--- identical canonical spec, fields reordered: cache hit, 0 charge ---
  count(city=Beijing & age>=18)              value=     92.34 reused=True  spent=0.50 (same value)
--- threshold 18 -> 30: spec changed, charged again ---
  count(age>=30 & city=Beijing)              value=     73.95 reused=False spent=1.00
--- bounded sum query, fresh charge 0.5 ---
  sum(income | active, clip[0,50k])          value=7560075.88 reused=False spent=1.50
--- new data version (n=401, new seed): same query is a cache miss ---
dataset=synthetic_users version=synthetic-seed20260928-n401-v1 records=401
  count(age>=18 & city=Beijing) @ v2         value=    107.34 reused=False spent=0.50
--- budget exhaustion: 1.5 left, new query wants 2.0 ---
  rejected: epsilon charge 2.0 exceeds remaining budget 1.5 (total 3, spent 1.5)
--- cached result still served for free after exhaustion ---
  count(age>=18 & city=Beijing) cached       value=     92.34 reused=True  spent=1.50
```

Noisy values are reproducible with the fixed seeds used in `demo.py`.

## Test Results

```
$ python3.11 -m unittest discover -s tests -v
... (canonical spec: field order, threshold, bounds, validation)
... (budget: identical reuse, threshold/epsilon/version/bounds re-charge,
     exhaustion, leader refund + follower error)
... (concurrency: 12 identical threads -> one value, one charge)
... (mechanism: seeded reproducibility, unbiasedness/scale, zero sensitivity)
... (synthetic-only enforcement, generator determinism/versioning)
----------------------------------------------------------------------
Ran 17 tests in 1.633s

OK
```
