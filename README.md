# Mergeable Reservoir Sampling

Python 3.11, standard library only. Uniform `k`-sampling over a stream of
**unknown length**, with summaries that **merge hierarchically** and keep
accepting appends.

## Algorithm

Priority-key sampling (A-Res style): every stream item gets an i.i.d.
`Uniform(0, 1)` key from a **caller-injected** random source; each summary
keeps the `k` items with the smallest keys. Merging is "union, keep the
`k` smallest keys" — never concatenation of raw samples, so small shards
are **not** over-represented. An item's survival depends only on its key,
never on shard sizes or merge-tree shape.

Each retained item carries its **priority key** and a unique identity
`(shard_id, seq)` — this is the merge evidence:

- **Associative / commutative**: any merge-tree shape equals the central run.
- **Idempotent**: items dedupe by identity and shard lengths merge
  append-only (max), so retried or diamond-path merges never double-count.
- **Appendable**: a merged summary can keep ingesting new items under its
  own fresh `shard_id`.

## Files

- `reservoir_sampling.py` — the `ReservoirSample` implementation.
- `test_reservoir_sampling.py` — 20 `unittest` tests (exact + empirical).
- `demo.py` — runnable example (`python3.11 demo.py`).

## Usage

```python
from reservoir_sampling import ReservoirSample

shard = ReservoirSample(k=5, shard_id="shard-0", seed=20260927)
shard.add_all(stream_prefix)          # stream of unknown length
root = ReservoirSample.merged([left, right], shard_id="root", seed=1)
root.add_all(more_items)              # merged summaries keep appending
root.sample()                         # the k sampled values
root.metadata()                       # k, n, sample_size, seed, rng_draws, ...
```

The random source is injectable: pass `rng=callable` returning floats in
`[0, 1)`; otherwise `random.Random(seed)` is used and the seed is recorded
in `metadata()` together with the sample size.

## Semantics

- Stream shorter than `k` → the sample is the whole stream.
- Unequal shard sizes → every item still has inclusion probability `k/n`.
- `k = 0` → nothing is stored, no keys drawn; only lengths are counted.

## Capability boundaries (explicit)

- **Length correction** (`correct_length`): fixes the reported total `n`
  after an upstream recount. The *sample* is unaffected (keys never depend
  on lengths). Boundary: corrections are **local** — shard lengths merge
  append-only (max), so a later merge carrying a larger stale length for
  the same shard overwrites the correction. Apply corrections at the
  authoritative shard or the final aggregation point. Correcting below the
  number of retained items of that shard is rejected.
- **Retraction** (`retract`): withdraws one item by identity and
  decrements its shard's length. Boundary: items already evicted from the
  reservoir **cannot be restored**, so after a retraction the sample may
  hold fewer than `min(k, n)` items. Exact fixed-size uniformity is only
  guaranteed for the append-only model.

## Exact vs empirical conclusions

- **EXACT** (proved by exhaustive enumeration of all `n!` key assignments
  on small streams): every `k`-subset is exactly equiprobable
  (`test_every_k_subset_equiprobable_central`); every shard layout and
  merge-tree shape yields a sample **identical** to central processing
  (`test_merge_trees_match_central_exactly`); merge-then-append equals
  central (`test_hierarchical_merge_then_append_matches_central`); retried
  merges are idempotent (`test_retry_merge_is_idempotent`).
- **EMPIRICAL** (seeded-PRNG estimates with stated tolerance, not proofs):
  subset frequencies within 5σ of expectation over 60 000 trials
  (`test_seeded_prng_subset_frequencies`).

## Test results

```
$ python3.11 -m unittest test_reservoir_sampling -v
...
Ran 20 tests in ~1.1s

OK
```

## Demo output

```
=== 1. Sharded stream, hierarchical merge vs central (seeded) ===
central  : ['event-559', 'event-131', 'event-443', 'event-785', 'event-362']
merged   : ['event-559', 'event-131', 'event-443', 'event-785', 'event-362']
identical: True
metadata : {'k': 5, 'n': 1000, 'sample_size': 5, 'seed': None, 'rng_draws': 0,
            'shard_lengths': {'shard-0': 100, 'shard-1': 250, 'shard-2': 50, 'shard-3': 600}}

=== 2. Retry / duplicate merge is idempotent ===
sample and n unchanged after duplicate merges: True
n = 1000 (still 1000, not 1000 + re-merged lengths)

=== 3. EXACT: all key assignments, n=4, k=2 (24 permutations) ===
  subset (0, 1): 4 of 24
  subset (0, 2): 4 of 24
  subset (0, 3): 4 of 24
  subset (1, 2): 4 of 24
  subset (1, 3): 4 of 24
  subset (2, 3): 4 of 24
  -> every k-subset equally likely: EXACT (enumerated, not estimated)

=== 4. EMPIRICAL: seeded PRNG frequencies, n=6, k=3, 60000 trials ===
  20 subsets, expected 3000 each; observed min=2896, max=3113
  -> consistent with uniformity: EMPIRICAL estimate, not a proof
```
