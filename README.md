# weighted-quantiles

Exact weighted quantiles and weighted empirical distributions in pure
Python 3.11 (standard library only).  Backed by a randomized treap whose
nodes cache cumulative subtree weights, so updates and quantile/ECDF
queries touch a single root-to-leaf path — samples are never re-sorted.

## Quantile definition

For the weighted ECDF `F(x) = (weight of observations <= x) / (total
weight)`, the q-quantile is the set `{ x : F(x-) <= q <= F(x) }`:

* unique answer -> returned as a degenerate interval `[v, v]`;
* `q` exactly on a cumulative-weight jump boundary -> the interval
  `[v_i, v_{i+1}]` of the two adjacent observed values (every value in
  between is a valid quantile);
* `q = 0` / `q = 1` -> the minimum / maximum positive-weight value.

With equal weights this degenerates to the ordinary inverse-empirical-CDF
definition (Hyndman-Fan type 1), with intervals at exact boundaries.

## Semantics

* weights are exact rationals (`fractions.Fraction`); negative weights are
  rejected, zero-weight observations are eliminated;
* duplicate values merge their weights in one tree node;
* `upsert` with an identical payload is an idempotent no-op; a changed
  payload atomically replaces the old observation, keeping the total
  weight consistent;
* retraction by observation ID, by subject, and by sliding-window expiry
  (`advance(now)` removes observations with `timestamp <= now - window`);
* stores can be sharded (`split_shards`) and merged (`merge_shards`);
  identical duplicate IDs merge idempotently, conflicting ones raise;
* treap priorities come from a caller-injected `random.Random`, so a
  given seed reproduces the exact structure and the node-visit counts.

## Exact vs. empirical labels

Results computed from the full population carry `label="exact"`
(`QuantileResult`).  Conclusions drawn from sampled random transactions
(`run_random_transactions`) carry `label="empirical"` and always record
the seed and the sample size.

## Layout

* `weighted_quantiles/treap.py` — treap with cumulative rational weights
  and node-visit instrumentation;
* `weighted_quantiles/store.py` — observation store: idempotent upsert,
  subject retraction, sliding window, shard split/merge;
* `weighted_quantiles/reference.py` — independent naive reference that
  fully re-sorts on every query (cross-check oracle);
* `weighted_quantiles/fuzz.py` — random-transaction harness comparing
  store vs. reference (upserts, retractions, subject retraction, window
  expiry, zero-weight and duplicate-ID transactions);
* `tests/` — unittest suite; `examples/demo.py` — runnable walkthrough.

## Run

```sh
python3 -m unittest discover -s tests -v
python3 examples/demo.py
```
