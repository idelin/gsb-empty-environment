# permtest

Permutation tests over **observation identities** (Python 3.11+, standard
library only, tested with `unittest`).

## Design principles

- **Observation identity, not values.** The legal permutation group is
  defined over observation *indices*. Tied (duplicate) values never reduce
  the group size or the probability multiplicity of any assignment.
- **Three designs.**
  - `independent-two-sample`: all `C(N, n1)` label assignments; unequal
    group sizes supported.
  - `paired-swap`: all `2^n` sign flips of within-pair differences.
  - `stratified`: labels permute within each stratum only; the group is
    the direct product `prod C(N_s, n1_s)`.
- **Explicit tail rules, equality included.**
  - `two-sided`: `|T| >= |T_obs|`
  - `greater`: `T >= T_obs`, `less`: `T <= T_obs`
- **Original arrangement counts.** Rank 0 of every space is the observed
  labelling, so the exact p-value is at least `1 / size`.
- **Exact vs. approximate are labelled.** Exact results carry
  `method="exact (full enumeration)"`; Monte Carlo results carry
  `method="monte-carlo (approximate)"` plus a Wilson score confidence
  interval -- never a pseudo-exact value.
- **Caller-injected randomness.** Monte Carlo takes `rng=` (a
  `random.Random`) or `seed=`; the seed and draw count are recorded on
  the result.
- **Sharded, resumable enumeration.** The rank range `[0, size)` splits
  into shards accumulated independently; `checkpoint_path` + `resume=True`
  continues an interrupted run.

## Usage

```python
from permtest import two_sample_test, paired_test, stratified_test

# Exact for small spaces (auto), Monte Carlo for large ones.
two_sample_test([8.1, 7.9, 8.4], [6.2, 5.8, 6.6])
two_sample_test(g1, g2, method="monte-carlo", draws=20_000, seed=42)

paired_test(before=[10, 12, 9], after=[11, 13, 10])
stratified_test([([8, 9], [5, 6]), ([7, 8, 9], [4, 5])])

# Sharded exact enumeration with a resumable checkpoint.
two_sample_test(g1, g2, method="exact", n_shards=8,
                checkpoint_path="ckpt.json", resume=True)
```

## Layout

- `permtest/spaces.py` -- permutation spaces over observation indices,
  combinatorial rank/unrank (lexicographic, `itertools`-compatible).
- `permtest/exact.py` -- tail rules, sharded enumeration, resumable
  accumulator, checkpointing, `ExactResult`.
- `permtest/montecarlo.py` -- seeded sampling, Wilson interval,
  `MonteCarloResult`.
- `permtest/api.py` -- `two_sample_test` / `paired_test` /
  `stratified_test` with `method="auto"`.
- `tests/` -- `unittest` suite, including brute-force reference
  verification against raw `itertools.permutations` enumeration.
- `examples/run_examples.py` -- runnable demo of all modes.

## Running

```
python3 -m unittest discover -s tests -v   # 40 tests
python3 examples/run_examples.py
```

## Test results

```
Ran 40 tests in 0.426s

OK
```

The reference tests enumerate the full permutation group independently
(raw `itertools.permutations` over indices for the independent design,
`itertools.product` sign vectors for the paired design, and the product
of per-stratum combinations for the stratified design) and require
agreement with the ranked enumeration to 12 decimal places, for all
three tail rules, including cases with ties and unequal group sizes.
