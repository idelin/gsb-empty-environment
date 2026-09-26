# tracecheck — bounded visible-trace comparison of state machines

Compares the **visible behaviour** (finite trace sets) of two finite state
machines within a length bound. Internal state names are irrelevant: the
comparison works on *reachable subsets of states* (on-demand powerset
construction), never on a 1-to-1 state matching, so nondeterminism and tau
steps are handled natively and pure renaming never reports a difference.

Requires Python 3.11+, standard library only.

## Model

```python
from tracecheck import make_machine

m = make_machine(
    states={"s0", "s1"},
    alphabet={"login", "logout"},
    transitions={("s0", "login", "s1"), ("s1", "logout", "s0")},
    initial="s0",
    version="v1",
)
```

A transition label of `None` is a silent **tau** step. Models are validated
at construction (finite sets, known states, labels in the alphabet) and are
immutable.

## Comparison

```python
from tracecheck import compare_bounded, Verdict

result = compare_bounded(old, new, bound=4)
```

- `Verdict.DIFFERENT` — a **shortest** distinguishing visible sequence is
  returned in `result.witness`, with replayable `result.evidence`: the
  reachable subset after every prefix on both sides (non-empty on the side
  that has the trace, empty on the other).
- `Verdict.EQUIVALENT_WITHIN_BOUND` — every subset pair reachable within
  `bound` agreed on every visible action. Nothing is claimed beyond `bound`.
- `Verdict.UNKNOWN` — the `max_pairs` budget was exhausted before the
  bounded space was fully explored; no conclusion either way.

The search is a BFS over the product of the two on-the-fly
determinisations, so the first divergence found is shortest. Tau loops
terminate via fixpoint closure; the empty trace is always a trace of both
sides.

## Cache

`SubsetCache` stores explored subset-transition tables keyed by
`(version, fingerprint)`. Editing a model or bumping its version changes
the key, so stale entries are never served after an update;
`invalidate_version(v)` drops a superseded version explicitly.

## Cross-check oracle

`tracecheck.reference.visible_words` / `reference_compare` enumerate all
visible words up to the bound by brute force; the test suite checks the
product search against this oracle on 150 random models per run.

## Run

```sh
python3 demo.py                              # runnable samples
python3 -m unittest discover -s tests -t .   # test suite (23 tests)
```
