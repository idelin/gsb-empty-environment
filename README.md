# diag — multi-phase diagnostic aggregation over evolving documents

`diag` merges diagnostics produced by several analysis phases (lint,
syntax, style, …) while the underlying document keeps being edited.

## Merge identity

A diagnostic's identity is the triple **(file version, original range,
rule id)** (`diag.Key`):

- Same message text at different ranges → **not merged**.
- Same current-text range but different origin (version, original
  range) → **not merged**; dedup by current text range is unsound
  because each phase response is bound to the version it analyzed.
- Duplicate identities within one response collapse, keeping the
  maximum severity.

## Phase refresh

`Store.Refresh(phase, version, diags)` replaces exactly that phase's
contribution set — other phases are untouched. Refreshing with an
empty slice revokes the phase's contributions; refreshing at a newer
file version replaces the phase's older identities wholesale. The
merged severity of an identity is the **maximum** across contributing
phases.

## Edit-chain range mapping

`Document` records every applied `Edit`. `MapRange(from, r)` maps a
range from its analysis version forward through the edit chain:

- a range intersecting any edit's replaced region is **invalidated**
  (its text may have changed) and dropped from snapshots;
- untouched ranges keep their identity and only shift position;
- insertion at a range boundary leaves the range untouched (inserted
  text lands outside it).

## Verification

- `store_test.go` — merge semantics: identity, max severity, phase
  refresh isolation, revocation, version updates, duplicate
  contributions.
- `document_test.go` — explicit range transforms: insertions,
  deletions, replacements, multi-edit chains, boundary conventions.
- `model_test.go` — a full-set reference model recomputed from every
  refresh record is compared against the store after each of 400
  randomized steps (edits, stale responses, revocations, duplicates);
  plus a full re-analysis check: after many edits with no re-refresh,
  every surviving diagnostic must reappear in a fresh analysis of the
  current text, and every fresh diagnostic missing from the snapshot
  must intersect an edited region.

Run: `go test ./...`
