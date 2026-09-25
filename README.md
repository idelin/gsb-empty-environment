# Evidence Coverage Engine

Python 3.11, standard library only. Computes satisfied intervals and gaps
for conclusion rules that require several evidence categories, each with
"at least k independent sources", with cross-category source exclusivity.

## Semantics

- Time is integral; evidence covers the half-open interval `[start, end)`,
  so exactly touching boundaries leave no gap.
- Duplicate evidence from the same source counts once per category.
- Evidence with `expires_at <= t` is ineligible at `t` (expired evidence
  cannot fill a requirement).
- With `exclusive_sources=True` a source counts toward at most one
  category at a time; satisfaction is a joint max-flow assignment, not a
  per-category count.
- The endpoint sweep evaluates each atomic segment once and merges
  adjacent satisfied segments, so retracting the last supporter shrinks
  intervals exactly at the true boundary.
- `minimal_subset` enumerates subsets in increasing size, so the first
  feasible subset is provably minimum. If the enumeration budget runs
  out, a feasible irreducible witness and the unproven optimum are
  reported separately (`proven_optimal=False`).
- Every fact, rule and step carries a stable ID and a version; scenarios
  are replayed deterministically and fingerprinted with a SHA-256 digest.

## Layout

- `evengine/model.py` — `Evidence`, `CategoryRequirement`, `Rule`
- `evengine/engine.py` — endpoint sweep, joint max-flow check, intervals/gaps/longest
- `evengine/subsets.py` — exhaustive minimal-subset search + greedy witness
- `evengine/replay.py` — versioned step replay and digest
- `sample_data.py` — synthetic scenario (all data fabricated)
- `main.py` — runs the sample scenario
- `test_evengine.py` — unittest suite

## Run

```sh
python3.11 main.py        # run the synthetic sample
python3.11 -m unittest -v # run the tests
```
