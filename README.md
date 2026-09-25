# Versioned Fact Arbitration

Python 3.11 standard-library implementation with no third-party dependencies.

## Semantics

- Each fact has stable `fact_id` and `version`; evidence has stable `evidence_id` and `version`.
- Evidence carries reliability `1 < 2 < 3`.
- Facts use a half-open UTC interval: `valid_from <= query_at < valid_until`.
- The highest active reliability level wins.
- Equal-level `true` and `false` claims remain `undecided`; neither side is arbitrarily chosen.
- Expired or future high-level facts cannot block lower-level active facts.
- Facts are canonically sorted before evaluation, so input order cannot affect output.
- Selected, suppressed, and inactive evidence all include machine-readable reasons.
- Rules and steps carry stable IDs and semantic versions.
- The runnable example uses synthetic data marked `synthetic: true`.

## Reason Codes

- `SELECTED_BY_HIGHEST_TIER`: selected by the highest active reliability level.
- `LOWER_TIER_NOT_PREFERRED`: active, but a higher active level was selected.
- `PEER_TIER_CONFLICT_UNRESOLVED`: equal-level conflicting evidence cannot be resolved.
- `BLOCKED_BY_UNRESOLVED_PEER_CONFLICT`: lower level blocked by top-level peer conflict.
- `EXPIRED`: `query_at >= valid_until`.
- `NOT_YET_EFFECTIVE`: `query_at < valid_from`.

## Replay

`replay.canonical_operation` contains the complete input, query time, and rule/step versions.
`replay.digest` is its canonical SHA-256 digest. `replay(operation)` recomputes the digest and
reruns resolution. Tampering with facts or rule/step metadata changes the digest or is rejected.

## Files

- `fact_resolver.py`: engine and immutable data model.
- `sample.py`: synthetic-data demonstration.
- `test_fact_resolver.py`: unittest suite.

## Run

```bash
python3.11 sample.py
python3.11 -m unittest -v
```

## Sample Result

```text

=== high tier active (2026-03-01T00:00:00Z) ===
claim sensor.triggered: status=undecided value=None winning_tier=2
claim switch.armed: status=decided value=False winning_tier=3
  selected   F-PRIORITY-HIGH: SELECTED_BY_HIGHEST_TIER
  suppressed F-DEADLOCK-F: PEER_TIER_CONFLICT_UNRESOLVED
  suppressed F-DEADLOCK-T: PEER_TIER_CONFLICT_UNRESOLVED
  suppressed F-DEADLOCK-LOW: BLOCKED_BY_UNRESOLVED_PEER_CONFLICT
  suppressed F-PRIORITY-MID: LOWER_TIER_NOT_PREFERRED
  suppressed F-PRIORITY-LOW: LOWER_TIER_NOT_PREFERRED
replay_digest=3250692d206ad4a3f6077a3b7f635c67168f952faf3cd3b41c720755da9fe35d
replay_matches=True

=== high tier expired; lower tier is effective again (2026-09-01T00:00:00Z) ===
claim sensor.triggered: status=undecided value=None winning_tier=2
claim switch.armed: status=decided value=True winning_tier=2
  selected   F-PRIORITY-MID: SELECTED_BY_HIGHEST_TIER
  suppressed F-DEADLOCK-F: PEER_TIER_CONFLICT_UNRESOLVED
  suppressed F-DEADLOCK-T: PEER_TIER_CONFLICT_UNRESOLVED
  suppressed F-DEADLOCK-LOW: BLOCKED_BY_UNRESOLVED_PEER_CONFLICT
  suppressed F-PRIORITY-LOW: LOWER_TIER_NOT_PREFERRED
  inactive   F-PRIORITY-HIGH: EXPIRED
replay_digest=f86fd4383882ad6906d45c4a1ccf3f27c832c881988a61ac3217a31be09eeeda
replay_matches=True

order_independent= True
```

## Test Result

```text
test_all_rules_and_steps_have_stable_ids_and_versions ... ok
test_duplicate_fact_version_is_rejected ... ok
test_empty_input_is_replayable ... ok
test_expired_high_tier_allows_lower_tier_to_become_effective ... ok
test_future_fact_is_reported_inactive ... ok
test_highest_reliability_wins_and_lower_is_suppressed ... ok
test_input_order_does_not_affect_result_or_replay_digest ... ok
test_replay_recomputes_digest_and_detects_tampered_fact ... ok
test_replay_rejects_changed_rule_version ... ok
test_same_tier_true_false_conflict_is_undecided ... ok

----------------------------------------------------------------------
Ran 10 tests in 0.001s

OK
```
