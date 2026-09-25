# Versioned Valid-Time Timelines

Python 3.11, standard library only. Tests use `unittest`.

## Layout

- `temporal/timeaxis.py` — exact time primitives. `WallClock` (tz-aware civil
  reading), `TimePoint` (exact `Fraction` ticks on the synthetic axis) and
  `Duration` (exact tick lengths) are distinct, non-interchangeable types;
  mixing them in comparisons raises `TypeError`. `OffsetTable` is a synthetic
  piecewise-linear conversion table (anchor wall-clock ↔ time-point pairs
  plus an exact tick-per-second rate); all conversions are exact.
- `temporal/core.py` — `Timeline` (immutable, coalesced half-open segments),
  `diff` (minimal continuous change segments; INSERT / DELETE / UPDATE),
  `apply` / `invert` (reversible deltas), `summarize` (baseline summary:
  SHA-256 digest + node count + span), and `Delta` (identified changes with
  correction and retraction).
- `temporal/merge.py` — `merge3` (three-way merge of two independent branch
  deltas) and `rebase`. Incompatible overlapping value changes yield minimal
  conflict segments: exactly the disagreeing overlap, not the whole span.
- `temporal/verify.py` — full-scan verification over the union of the
  original endpoints (`scan_equal`, `verify_apply`, `verify_inverse`,
  `verify_summary`).
- `demo.py` — runnable end-to-end sample.
- `tests/` — 46 unittest cases.

## Semantics

- **Minimal change segments**: elementary intervals between consecutive
  endpoints coalesce only when `(kind, old, new)` all match — adjacent
  same-value segments merge, different change kinds never merge.
- **Zero-length changes** are never emitted and cannot be constructed
  (`Segment` and `Change` reject `start >= end`).
- **Reversible**: `apply(result, invert(delta))` restores the base exactly;
  verified by full endpoint scan and by the baseline summary digest.
- **Structural sharing**: unchanged nodes are reused by object identity
  between snapshots; `ApplyResult` records the actual `copied_nodes` and
  `shared_nodes` counts.
- **Corrections and retraction**: a correction carries the identity of the
  change it supersedes; `Delta.retract(identity)` withdraws it so the base
  state is restored from history — never by matching the current value.
- **Conflicts**: when both branches change an overlapping interval to
  incompatible values, the conflict region keeps its base value in the
  merged timeline and is reported as a `Conflict(start, end, base, a, b)`;
  non-conflicting parts of both branches still apply.

## Run

```
python3.11 demo.py
python3.11 -m unittest discover -s tests
```

## Sample output (abridged)

```
=== Minimal diff: insert / delete / update, no zero-length output
diff(base, edited): 4 minimal change segment(s)
  update [4, 10) 'idle' -> 'starting'
  update [14, 16) 'running' -> 'paused'
  delete [20, 30) 'cooldown' -> MISSING
  insert [30, 36) MISSING -> 'off'

=== Reversible application + baseline summary verification
apply(base, delta) == edited: True
apply(result, inverse) restores base: True
restored matches baseline summary: True

=== Structural sharing: old snapshot keeps its nodes
single-segment edit -> copied nodes: 1, shared nodes: 2 (of 3)

=== Three-way merge of two independent branch deltas
merged ok: False
  minimal conflict [10, 12): base='running' A='turbo' B=MISSING
  minimal conflict [12, 18): base='running' A='turbo' B='eco'

=== Identified corrections and retraction
after retracting fix-1: Timeline([0, 10)->'idle', [10, 20)->'running', [20, 30)->'cooldown')
retraction restored the base exactly: True
```

## Test results

```
$ python3.11 -m unittest discover -s tests
..............................................
----------------------------------------------------------------------
Ran 46 tests in 0.018s

OK
```
