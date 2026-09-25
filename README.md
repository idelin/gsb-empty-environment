# fact-engine

Deterministic fact-resolution engine. Python 3.11, standard library only.

## Model

- **Fact** (`fact_id`, `version`): asserts a boolean `value` for a `claim`,
  carries a `source_level` (higher = more reliable) and a half-open validity
  interval `[valid_from, valid_to)` (`valid_to=None` means open-ended).
- **Rule** (`rule_id`, `version`): the versioned ruleset in
  `fact_engine/model.py` (`DEFAULT_RULES`).
- **Step** (`step_id`, `version`): each resolution records four stable steps,
  each tagged with the rule id/version it applied.

## Resolution pipeline (`fact_engine/engine.py`)

1. `step-01-version-supersede` — only the highest version of each fact id is
   current; older versions are suppressed (`superseded-by-newer-version`).
2. `step-02-validity-window` — facts outside their interval at the query
   moment are suppressed (`not-yet-effective` / `expired`). When a high-level
   fact expires, lower-level facts become effective again automatically.
3. `step-03-prefer-high-level` — only the highest effective `source_level`
   may decide; lower levels are suppressed (`outranked-by-higher-level`).
4. `step-04-same-level-conflict` — if top-level facts assert both `true` and
   `false`, the claim is **UNDECIDED**; both sides are kept as `contesting`
   and neither is picked.

Every suppressed fact is reported with its reason. Facts are re-sorted by
stable keys before each step, so input order never changes the outcome
(verified over all permutations in tests and `sample.py`).

## Replayable evidence (`fact_engine/replay.py`)

`build_evidence()` captures facts, rules, query moment, engine version, the
canonical decision and two SHA-256 digests (`input_digest` over the inputs,
`digest` over the decision). `verify()` re-runs the engine from the bundle
and fails on any tampering with inputs, decision, or engine identity.

## Run

```
python3.11 sample.py                      # synthetic end-to-end demo
python3.11 -m unittest discover -s tests -v
```

All sample data is synthetic.
