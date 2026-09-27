# Certified-Envelope Rejection Sampling

Python 3.11, standard library only.

## Guarantees

- **Explicit envelope**: every partition bound is caller-certified or exactly
  verified over a finite domain; uncertified bounds are rejected
  (`CertificateRequiredError`).
- **No clamping**: if `target / (bound * proposal)` exceeds 1 at any attempt,
  sampling aborts with `EnvelopeViolationError`; no unbiased-sample claim is
  made after a violation.
- **Adaptive refinement**: partitions can be split into finer certified
  sub-partitions (`PartitionedEnvelope.refine`), atomically.
- **Budget & cancellation**: `sample(n, budget=..., cancel=...)` returns
  partial results (`budget_exhausted` / `cancelled`) with completed samples
  and the remaining count.
- **Recoverability**: `sampler.state` / `sampler.restore(state)` checkpoint
  the RNG state and confirmed acceptance positions (attempt indices).
- **Statistics**: acceptance rate denominator is always the attempt count;
  exact (finite enumeration) and empirical (sampled) quantities are labelled
  `"exact"` / `"empirical"`.
- **Caller-injected randomness**: the RNG is injected; the seed is recorded
  on every result as audit metadata.

## Files

- `rejection_sampling.py` — library
- `test_rejection_sampling.py` — unittest suite (21 tests)
- `demo.py` — runnable end-to-end example

## Run

```sh
python3.11 -m unittest test_rejection_sampling -v
python3.11 demo.py
```
