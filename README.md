# hmm — finite-HMM toolkit (Python 3.11, standard library only)

Exact inference for finite hidden Markov models with an emphasis on
correctness at the edges: scaled recursions for long sequences, explicit
infeasibility for zero-probability sequences, versioned incremental updates,
and brute-force cross-checks on small models.

## Layout

- `hmm/model.py` — `HMM` / `PiecewiseHMM` definitions and parameter validation
  (rows must be non-negative and sum to 1; zero rows are rejected, never
  silently renormalised). `None` observations are missing data.
- `hmm/inference.py` — scaled forward-backward (posteriors + marginal
  log-evidence), log-space Viterbi, pointwise MAP, top-K distinct paths,
  and `accumulate_log_probability` (probability-mass accumulation kept
  separate from path ranking).
- `hmm/incremental.py` — `VersionedObservations`: local observation revisions
  reuse unchanged forward/backward messages; every version stays queryable.
- `hmm/enumeration.py` — exhaustive path enumeration used as ground truth
  for the dynamic-programming implementations.
- `hmm/sampling.py` — trajectory sampling with a caller-injected
  `random.Random`; empirical estimates record seed and sample size and are
  labelled `kind="empirical"` (exact results are labelled `[exact]` in the
  examples).
- `tests/` — `unittest` suite (40 tests).
- `examples.py` — runnable demo of every feature.

## Conventions

- Empty sequence: evidence = 1.0 (log-evidence 0.0), posteriors `[]`,
  Viterbi path `[]`.
- Zero-probability sequence / impossible observation: `feasible=False`,
  posteriors `None`, log-evidence `-inf`, Viterbi path `None` — never a
  renormalised pseudo-posterior.
- The Viterbi path is the best single path; concatenating per-time MAP
  states is in general a different (possibly worse) path.

## Run

```
python3.11 -m unittest discover -s tests -v
python3.11 examples.py
```

## Sample output (`python3.11 examples.py`, abridged)

```
=== forward-backward (missing observation at t=4) ===
feasible=True  log-evidence=-5.512669 [exact]
  t=0 obs=walk   P(state|obs)=[0.8815, 0.1185]
  ...
=== Viterbi vs pointwise MAP ===
viterbi path=[0, 1, 1, 0, 0, 0]  logP=-7.690327 [exact]
MAP concat =[0, 0, 1, 0, 0, 0]  (need not equal viterbi)
=== long sequence scaling (T=20000, no underflow) ===
log-evidence=-23375.390 (finite, < 0) [exact]
=== incremental revision with versioned queries ===
v0 log-evidence=-5.512669 [exact]
v1 (obs[2]: clean->walk) log-evidence=-4.870770 [exact]
v0 still queryable: log-evidence=-5.512669
v2 impossible obs -> posteriors=None log-evidence=-inf
=== MAP concat != Viterbi (constructed example) ===
MAP concat=[0, 1, 1] viterbi=[0, 1, 0] [exact]
=== sampling with caller-injected RNG; empirical vs exact ===
[empirical] kind=empirical seed=20261002 n=20000
[empirical] P(s_0)=[0.5944, 0.4056]
[exact]     P(s_0)=[0.6, 0.4]
```

## Test results

```
$ python3.11 -m unittest discover -s tests -v
...
Ran 40 tests in 0.177s

OK
```

The suite cross-checks forward-backward, Viterbi and top-K against full
path enumeration on small HMMs, verifies scaling on a 20 000-step sequence,
covers missing observations, hard state constraints, piecewise transitions,
incremental revisions (including restoring feasibility), zero-probability
sequences, impossible observations, empty sequences, and reproducibility of
the caller-seeded sampler.
