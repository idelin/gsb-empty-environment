# Rule Coverage & Conflict Analyzer

Analyzes priority-ordered rule sets whose conditions are **finite
enumerations** (`EnumSet`) or **inclusive integer intervals** (`IntRange`).
Because every condition space is finite, all evidence is replayable by
deterministic re-evaluation.

## What it reports

- **Coverage relations**: pairs `(A, B)` where every input matched by `B`
  is also matched by `A`.
- **Unreachable rules**: a lower-priority rule fully covered by
  higher-priority rules (single-rule subsumption, or exact union coverage
  via bounded enumeration) is marked `unreachable`.
- **Same-priority conflicts**: overlapping rules with equal priority and
  different effects; a concrete counterexample input inside the overlap is
  emitted.
- **Partial overlaps**: only the overlap counterexample is reported
  (`partial_shadow`); the lower-priority rule stays reachable.

## Budget semantics

Every pairwise check and every enumerated point consumes budget. If the
budget is exhausted the report is marked `INCOMPLETE` — the analyzer never
claims that conflicts or unreachable rules are absent in that case.

## Stable ids, versions, replay

- Facts (`FactSchema.fact_id`/`version`), rules (`rule_id`/`version`) and
  analysis steps (`step_id`/`version`) are all versioned; rule references
  use the form `R-ID@vN`.
- Findings carry a content-hashed stable `finding_id` and the analyzer
  version; identical inputs yield identical ids.
- `replay(finding, rules, schema)` re-executes the evidence: conflict and
  partial-shadow findings re-match the counterexample; unreachable
  findings re-enumerate the covered space.

## Usage

```sh
python3.11 run_sample.py        # synthetic end-to-end demo
python3.11 -m unittest -v       # test suite
```

## Files

- `model.py` — conditions, versioned facts/rules, rule evaluation.
- `analyzer.py` — `analyze()` and `replay()`.
- `run_sample.py` — synthetic loan-application sample (also demonstrates
  budget starvation).
- `test_analyzer.py` — unittest suite.
