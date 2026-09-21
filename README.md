# Cross-source nearest-sample alignment

`aligner.py` — incremental as-of join of one main stream against any number of
auxiliary streams, with versioned corrections and watermark finalization.

## Semantics

- Each main event is an independent **anchor** at time `t`.
- Each aux stream contributes at most one sample per anchor: the nearest with
  `|dt| <= tolerance`; ties break by earlier `time`, then smaller `seq`.
- Output rows carry anchor id, per-source sample id, missing flag, and a
  per-anchor `version` (1, 2, 3, ...). A late better sample or a retraction
  that changes the best candidate emits a replacement `AlignedResult` with a
  bumped version. `RetractResult` removes a row, `FinalizeResult` locks it.
- **Finalization**: an anchor at `t` is locked once `main_wm >= t` and every
  `aux_wm >= t + tolerance`. Locked rows ignore late upserts and retractions.
- **GC**: a sample is dropped only when no future anchor can reach it
  (`sample.time + tol <= main_wm`) **and** no live (unclosed) anchor sits
  within tolerance — a live anchor may still need it as a fallback after a
  retraction.

## Interfaces

- Incremental: `Aligner.apply(event) -> [OutputEvent]`
- Batch: `Aligner.apply_all(events) -> [OutputEvent]`
- Consumer: `AlignmentTable.apply_all(output_events)` rebuilds the current
  table from the event log alone (see `demo.py`).
- `reference.py` is an independent full-scan implementation used by the
  randomized tests to cross-check random arrival orders.

## Boundaries

- Watermark regression raises `ValueError`; unknown sources raise `ValueError`.
- Events with `time <= watermark` of their stream violate the contract; the
  aligner stays internally consistent but equivalence with the scan reference
  is only guaranteed for contract-respecting streams.
- Retractions of unknown ids are no-ops; re-upsert of an existing id replaces.
- The finalized-anchor id set grows with finalized anchors (one id per row);
  bounded in practice by downstream retention of the output log.

## Cost

- Aux upsert/retract: `O(log S + K * S_src * log S)` where `S` = buffered
  samples, `K` = live anchors inside the tolerance window, `S_src` = #sources.
- Main upsert: `O(S_src * log S)`. Watermark: `O(F + GC)`.
- GC: `O(S * log A)` per pass, runs on every watermark advance; `A` = live anchors.
- Memory: `O(A + S + finalized ids)`.
- Measured: 20k mixed events (10k anchors, 2x10k samples, 2 aux streams)
  processed in ~0.36s; finalization + full GC of 20k samples in ~0.05s.

## Tests

`python3 -m pytest test_aligner.py` — 311 tests: the spec example, tie-breaks,
shared samples, retraction fallback, out-of-tolerance miss, anchor retraction,
watermark locking, GC safety, and 300 seeded random-order cross-checks against
the scan reference (plus per-anchor version-monotonicity invariants).
`python3 demo.py` walks the spec scenario end to end.
