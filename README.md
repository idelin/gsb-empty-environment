# Sharded Gradient Accumulation with Atomic Commit

Pure Python 3.11, standard library only (plus `unittest` for tests). No
autodiff, no tensor libraries: `tensor.py` implements the small dense
1-D tensor type the system needs.

## Design

| File | Role |
| --- | --- |
| `tensor.py` | Minimal 1-D float tensor (`Tensor`, `add`, `is_finite`). |
| `optimizer.py` | SGD with momentum. `compute_update` is pure; `apply_update` swaps fully-computed state in, so a failed update never partially writes parameters or momentum. |
| `accumulator.py` | Micro-batch contributions keyed by `(batch_id, param_version)`. Out-of-order arrival, idempotent retry, withdrawal, stale-version rejection, explicit `clear()` zero semantics. |
| `coordinator.py` | Two-phase commit across shards with explicit `PREPARED` / published (`IDLE`) states; JSON checkpoint including unconfirmed batches and staged updates; `recover()` resolves crashes. |
| `faults.py` | Deterministic fault schedules: non-finite gradients, duplicate delivery, withdrawal, shuffled arrival order. |
| `reference.py` | Independent per-batch reference (shares no code with the system) used to cross-check final params, momentum, and step counts. |
| `demo.py` | End-to-end run sample under fault injection. |

## Semantics

- **Accumulate-then-commit**: `expected_microbatches` contributions must
  be present before `commit()` does anything (`NOT_READY` otherwise).
- **Overflow skip**: if any aggregated gradient is NaN/inf, the whole
  commit is skipped — parameters, momentum buffers, step count, and the
  parameter version are all left untouched, and the accumulator is
  cleared (no partial momentum is kept).
- **Atomicity**: every shard stages its update in `PREPARE`; only when
  all shards are prepared does `PUBLISH` swap state in. Any failure
  aborts all shards before anything is applied.
- **Idempotency & isolation**: re-submitting the same `batch_id` with
  identical gradients is a no-op; conflicting content raises; gradients
  are summed per active `batch_id` only, so batches never mix.
- **Versioning**: submissions carry the parameter version they were
  computed against; stale versions are rejected so a late retry from
  before a commit cannot leak into the next round.
- **Checkpoint**: captures params, momentum, step counts, param version,
  all unconfirmed micro-batch contributions, and staged-but-unpublished
  shard updates. `recover()` publishes when all shards had prepared,
  otherwise aborts the staged state.

## Run

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -t . -v
```

## Sample output (demo.py)

```text
group 1 (2nd micro-batch non-finite)  -> skipped_non_finite
  params unchanged: w=[1.0, -2.0], b=[0.5], steps=0
group 2 (reordered, b4 duplicated, b6 withdrawn) -> committed
group 3: checkpointed with unconfirmed batches ['g3-b10', 'g3-b8', 'g3-b9']
group 3 (restored from checkpoint, then commit) -> committed
group 4 (crash after prepare, recovered via checkpoint) -> steps=3
system   : params={'w': [0.6900000000000001, -2.339], 'b': [0.38159999999999994]}
reference: params={'w': [0.6900000000000001, -2.339], 'b': [0.38159999999999994]}
system   : momentum={'w': [1.1, 1.29], 'b': [0.42400000000000004]}
reference: momentum={'w': [1.1, 1.29], 'b': [0.42400000000000004]}
steps: system=3 reference=3
PARITY OK: final params, momentum and step counts match the independent reference
```

## Test results

`python3.11 -m unittest discover -s tests -t .` — **22 tests, OK**:

- `tests/test_numerical.py` — hand-computed values: single commit,
  momentum carry-over across commits, non-finite second micro-batch
  keeps every parameter/momentum/step at its old value, inf gradients.
- `tests/test_accumulator.py` — out-of-order arrival, idempotent
  duplicate, conflicting resubmit, withdrawal, stale version, clear
  zero semantics, per-batch isolation, snapshot round-trip.
- `tests/test_commit.py` — readiness gate, cross-shard atomic skip on
  overflow, no momentum pollution after a skip, stale retry rejection,
  version bump and accumulator clear on commit.
- `tests/test_checkpoint.py` — JSON checkpoint with unconfirmed batches,
  restore-then-commit parity, crash-after-prepare recovery (publish),
  partial-prepare recovery (abort).
- `tests/test_reference_parity.py` — randomized fault schedules
  (5 seeds × 6 groups × 3 shards) against the independent reference:
  final parameters, momentum buffers, and step counts all match.
