# secexplain

Visibility-projected, replayable explanations for a tiny deterministic
forward-chaining reasoner. Python 3.11+, standard library only, tests
with `unittest`. All bundled sample data is synthetic.

## What it does

* Derives conclusions from facts and rules; every fact, rule and
  derivation step carries a **stable id and a version**.
* Generates an explanation of a conclusion from the **caller-visible
  fact set only** (projection to the caller's clearance).
* Hidden content is replaced by a single constant placeholder
  (`[RESTRICTED]`) and the explanation is **marked incomplete**
  (`complete: false`).
* Ships a **finite-domain pairwise noninterference tester**, a
  counterexample showing that naive field deletion still leaks, and the
  fixed projection that passes the same tests.
* Explanations are packaged as **replayable evidence** (schema version +
  SHA-256 digest + stable ids) that anyone can re-verify deterministically.
* The explanation cache is **isolated per clearance level**; debug
  traces follow the same projection rules.

## Security model

**Low-visibility input equivalence.** Two complete fact sets that differ
only in hidden parts (facts/rules above the caller's clearance) must
produce *identical* public explanations, *identical* public errors and
*identical* structural counts. The tester in `secexplain/noninterference.py`
checks this property pairwise over a finite domain of worlds.

How the projection (`secexplain/projection.py`) enforces it:

* Any number of hidden premises of a visible step collapse into **one**
  placeholder child — the *number* of hidden premises is not leaked.
* A hidden support chain of any length collapses into **one**
  placeholder edge — hidden chain length is not leaked via path depth.
* Structural counts are computed **after** projection and only ever
  describe visible structure.
* Public errors use one constant message, so "does not exist" and
  "exists but hidden" are indistinguishable.
* Step ids are stable and depend only on (rule id, rule version, output
  fact id, output fact version) — never on hidden input identities.

**Intentional disclosure (one bit).** `complete: false` reveals that
*some* content was withheld. This is required so callers know the
explanation is incomplete, and it is the only hidden-dependent bit in
the public output.

**Explicitly out of scope.** Timing side channels are **not** covered:
no promise is made about running time, cache-hit latency, or other
timing observations. If hidden facts change *which* visible conclusions
are derivable at all, that is a genuine information flow no projection
can hide (the tester will report it as a violation).

## The counterexample

`secexplain/naive.py` projects by plain field deletion: hidden facts are
removed, but array slots, premise counts, hidden support depths and
derivation-wide totals are kept. Two low-equivalent worlds then produce
different public outputs (e.g. `hidden_step_count` differs), which the
tester detects. `tests/test_noninterference.py` demonstrates the leak
and verifies that the fixed projection has zero violations on the same
domain.

## Layout

```
secexplain/
  model.py            facts, rules, steps (stable ids + versions)
  engine.py           deterministic forward chaining, stable step ids
  projection.py       visibility projection (the security-critical part)
  naive.py            INSECURE naive projection (counterexample only)
  noninterference.py  finite-domain pairwise noninterference tester
  cache.py            per-clearance isolated explanation cache
  replay.py           replayable evidence build/verify
  sampledata.py       synthetic sample worlds
examples/run_sample.py  runnable demo
tests/                  unittest suite
```

## Run

```sh
# demo (synthetic data)
python3.11 -m examples.run_sample

# tests
python3.11 -m unittest discover -s tests -t . -v
```
