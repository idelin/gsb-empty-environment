# Shared-Rule DAG Support-Set Maintenance

Maintains **inclusion-minimal fact support sets** for goals over a shared
rule DAG. Pure Python 3.11 standard library; tests use `unittest`.

## Files
- `supportdag.py` — model (facts/rules/literals with stable ids + versions),
  derivation engine with incremental invalidation, and a lazy cost-ordered
  enumerator with budgets and pause/resume checkpoints.
- `verifier.py` — independent verifier: replays each support set to prove
  sufficiency and non-redundancy, emitting content-hashed proof steps that
  can be re-validated against the evidence base (`replay_proof`).
- `demo.py` — runnable synthetic walkthrough of every feature.
- `test_supportdag.py` — unittest suite (23 tests).

## Semantics
- A rule head is a goal; each rule is a disjunction of alternatives, each
  alternative a conjunction of positive facts, sub-goals and negated facts.
- A support `(pos, neg)` pins facts to the exact versions used as evidence
  plus the fact ids that must stay absent. `{A}` dominates `{A,B}`; only
  inclusion-minimal supports survive (inclusion-minimal ≠ minimum
  cardinality — both `{A}` and `{C,D}` can be minimal).
- Fact retraction/version bumps and rule version changes invalidate exactly
  the dependent goals via a reverse dependency index.
- Ungrounded cycles derive nothing: goals already on the derivation path are
  cut, and cycle-tainted results are never cached.
- Enumeration is lazy and cost-ordered (non-negative costs ⇒ subsets surface
  before supersets, so dominance pruning is sound). Budget truncation
  (`max_results` / `max_cost`) reports `complete=False` instead of silently
  omitting supports. `checkpoint()`/`resume()` serialize the enumerator;
  resume refuses to continue after any fact/rule version drift.

## Run
```
python3.11 demo.py            # synthetic end-to-end sample
python3.11 -m unittest -v     # test suite
```
