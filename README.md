# diagnostics

A Go library that merges diagnostics from multiple analysis stages and
keeps their ranges valid across document edits.

## Identity and merging

A diagnostic's identity is the triple **(file version, original range,
rule ID)** — the version the analysis ran against and the range in that
version's coordinates. Two contributions merge only when all three agree:

- identical message text at different ranges never merges;
- the same current text range reported against different analysis
  versions never merges (dedup by current text range is impossible by
  construction);
- merged severity is the **maximum** across contributions; stages and
  distinct messages are unioned.

## Stages

Each stage owns exactly one contribution slot per file, bound to the
analysis version it computed from:

- `Submit(stage, version, diags)` replaces only that stage's
  contribution; other stages are untouched;
- an empty `diags` (or `Revoke`) withdraws the stage's contribution;
- stale results (`version` older than current) are accepted and mapped
  forward through the recorded edit chain;
- results from the future are rejected with `ErrFutureVersion`.

## Edits and range mapping

`ApplyEdits` records one edit batch and advances the file version. A
diagnostic whose content an edit touches is invalidated; untouched
diagnostics keep their identity and are re-anchored to current
coordinates on read. An edit touches a range when their half-open
intervals overlap; insertions exactly at a range boundary are outside
the diagnostic. A zero-width range at `p` is invalidated only when the
replaced interval strictly contains `p`.

## Verification

- `mapping_test.go` — table-driven deletion/insertion cases plus a
  20k-iteration property test cross-checking `MapThroughEdits` against
  an independent token-document model.
- `store_test.go` — merge, refresh, revocation, version-binding,
  stale-response and full-reanalysis scenarios.
- `model_test.go` — 3000 random operations (submissions, revocations,
  edit batches, stale and future responses) with the merged output
  compared against a full-set model recomputed from scratch after every
  step.
