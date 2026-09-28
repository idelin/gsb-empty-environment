# Lineage Erasure Planner (synthetic demo)

Generates deletion / recompute plans from a subject-to-artifact lineage
graph. Python 3.11, standard library only; tests use `unittest`.

## Scope and disclaimers

- **Synthetic data only.** This is a planning demo, not a compliance product.
- **No formal privacy proof.** Any aggregate/statistical experiments here are
  illustrative; no differential-privacy or other formal guarantee is claimed
  or implied.
- **Published ≠ retracted.** Flagging a published statistic never claims
  external copies were recalled; a corrective re-publication is a *new*
  release, not a retraction.
- **Shared artifacts are never deleted.** Deleting them would destroy other
  subjects' data; they are recomputed without the erased subject, or sent to
  manual review when lineage is insufficient.

## Definitions

| Term | Meaning |
| --- | --- |
| `Subject` | Canonical id plus an explicit alias map. Unmapped identifiers fail loudly (`UnknownSubjectError`); no silent matching. |
| `ArtifactKind.ORIGINAL` | Original record ingested from a subject. |
| `ArtifactKind.CACHE` | Regenerable intermediate artifact. |
| `ArtifactKind.DERIVED` | Derived artifact (aggregate, feature, model). |
| `ArtifactKind.PUBLISHED` | Statistic already released externally. |
| `contributions` | Per-subject payloads (e.g. partial sums) enabling exact recompute. |
| `aggregator` | Function combining remaining contributions on recompute. |

## Plan actions

| Action | When |
| --- | --- |
| `DELETE` | ORIGINAL / CACHE / DERIVED solely from the erased subject. |
| `RECOMPUTE` | Shared artifact with `contributions` + `aggregator`; rebuilds excluding the subject, keeping everyone else's data. |
| `MANUAL_REVIEW` | Shared artifact lacking recompute lineage; never auto-deleted. |
| `MARK_UNRETRACTABLE` | PUBLISHED artifact; external copies may persist. |
| `REPUBLISH_CORRECTION` | Optional (`republish_correction=True`); a new corrected release, not a recall. |

## Parameters

- `Planner.plan_erasure(subject_ref, *, republish_correction=False)` —
  `subject_ref` may be a canonical id or any declared alias; the plan is a
  **read-only dry run** (`plan.dry_run is True`) and mutates nothing.
- `execute_plan(plan, graph, store)` — the only mutating entry point; applies
  the plan to an in-memory store of synthetic payloads and returns an
  `ExecutionReport`.

## Run

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

## Sample output (`demo.py`)

```text
=== DRY RUN (read-only; store untouched) ===
subject: subj-alice  aliases: ('alice@example.com', 'alice-device-7')
  delete                 raw:alice-events           original artifact solely derived from subject subj-alice
  delete                 cache:alice-session        cache artifact solely derived from subject subj-alice
  recompute              derived:daily-active       shared artifact; recompute excluding subject subj-alice, keeping data of 1 other subject(s)
  mark_unretractable     published:weekly-report    already published; external copies may persist and cannot be recalled
  republish_correction   published:weekly-report    issue a corrected re-publication computed without the subject; this is a new release, not a retraction
  manual_review          derived:joint-embedding    shared artifact lacks per-subject contributions or an aggregator; cannot recompute safely without risking other subjects' data
  WARNING: published:weekly-report: published statistic cannot be retracted from external parties; only internal records are updated
  WARNING: derived:joint-embedding: manual review required; insufficient lineage for safe recompute
store keys after dry run: ['cache:alice-session', 'derived:daily-active', 'derived:joint-embedding', 'published:weekly-report', 'raw:alice-events', 'raw:bob-events']

=== EXECUTE ===
  applied: delete raw:alice-events
  applied: delete cache:alice-session
  applied: recompute derived:daily-active
  note: published:weekly-report: flagged as published/unretractable; no external recall is claimed
  note: published:weekly-report: corrective re-publication planned as a new release; external copies of the original may persist
  note: derived:joint-embedding: left untouched pending manual review
store keys after execute: ['derived:daily-active', 'derived:joint-embedding', 'published:weekly-report', 'raw:bob-events']
derived:daily-active recomputed to: 3
```

## Test results

`python3.11 -m unittest discover -s tests -v` — 13 tests, all passing:

```text
test_alias_collision_rejected ... ok
test_alias_resolves_to_canonical_subject ... ok
test_artifact_sources_normalize_aliases ... ok
test_unmapped_identifier_fails_loudly ... ok
test_planning_is_read_only ... ok
test_execute_applies_deletes_and_recompute ... ok
test_execute_without_correction_step_publishes_nothing ... ok
test_optional_corrective_republication ... ok
test_other_subjects_artifacts_untouched ... ok
test_published_is_flagged_unretractable_not_deleted ... ok
test_shared_artifact_is_recomputed_never_deleted ... ok
test_shared_artifact_without_lineage_goes_to_manual_review ... ok
test_sole_original_and_cache_are_deleted ... ok

Ran 13 tests in 0.001s
OK
```
