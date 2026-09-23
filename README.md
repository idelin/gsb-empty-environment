# dfa-learning

Experimental library for learning an unknown deterministic finite automaton
from queries.  Pure Python 3.11 standard library; no automata-learning
packages are used.

The learner only sees two oracle interfaces — membership queries and bounded
equivalence queries.  It never accesses the hidden machine's states or
transition table; the test side holds the hidden machine and plays the
oracle.  The alphabet is finite and the query domain is every word of length
at most `L`.

## Layout

- `dfa_learning/dfa.py` — explicit DFA used for the hidden machine and for
  the executable hypothesis.
- `dfa_learning/oracles.py` — `MembershipOracle` and
  `BoundedEquivalenceOracle`.
- `dfa_learning/learner.py` — observation-table learner (L*-style) with a
  query budget, checkpoints, and external-counterexample handling.
- `dfa_learning/enumerator.py` — independent brute-force enumerator used to
  validate the oracle and the final hypothesis on small `L`.
- `tests/` — hidden-machine fixtures and the test suite.
- `examples/demo.py` — end-to-end run with per-round statistics and a
  fresh-vs-resumed comparison.

## How it works

**Observation table.** The learner keeps a closed, consistent table
`(S, E, T)`: access prefixes `S`, distinguishing suffixes `E`, and cells
`T[s, e] = membership(s·e)`.  Cells that cannot be determined yet are stored
as `None` and are never treated as rejection.  From a closed, consistent
table it builds an executable hypothesis DFA (states = distinct rows).

**Bounded equivalence oracle.** Instead of enumerating the exponentially
many words of length `<= L`, the oracle walks the product graph of the
hidden machine and the hypothesis breadth-first, expanding symbols in
alphabet order.  It visits at most `|Q_hidden| x |Q_hypothesis|` product
states and returns the shortest disagreeing path, breaking ties by
lexicographic order (with respect to the given alphabet order).

**Counterexample processing.** A counterexample is analysed by adding its
distinguishing suffixes to `E` one at a time.  Membership answers are cached
and reused, so re-derived evidence never costs a new query.

**Budget and checkpoints.** The budget counts new (uncached) membership
queries.  When it runs out — even in the middle of processing a
counterexample — `save_checkpoint` persists the table (including `None`
cells), the pending-suffix queue, the cache, and all counters.
`load_checkpoint` restores the run exactly; checkpoints are bound to the
ordered alphabet, `L`, and the oracle version, and mismatched evidence is
rejected with `CheckpointMismatchError`.

**External counterexamples.** `submit_counterexample(word)` first verifies
the word with a membership query against the current hypothesis.  Wrong,
out-of-domain, or unverifiable counterexamples are rejected and the table is
left untouched.

**Result semantics.** `learn()` returns `equivalent_in_domain` (the
hypothesis agrees with the hidden machine on every word of length `<= L` —
nothing is claimed about longer words), `budget_exhausted` (undecided; save
and resume), or `hypothesis_ready` (no equivalence oracle supplied).

## Usage

```python
from dfa_learning import (
    BoundedEquivalenceOracle, Learner, MembershipOracle,
)

membership = MembershipOracle(hidden_dfa, max_len=12, version="hidden-v1")
equivalence = BoundedEquivalenceOracle(hidden_dfa, ("a", "b"), max_len=12,
                                       version="hidden-v1")
learner = Learner(membership, ("a", "b"), max_len=12, budget=1000)
result = learner.learn(equivalence)
if result.status == "budget_exhausted":
    learner.save_checkpoint("checkpoint.json")
    # later, with a larger budget:
    learner = Learner.load_checkpoint("checkpoint.json", membership,
                                      budget=5000)
    result = learner.learn(equivalence)
```

## Tests and demo

```
python3 -m unittest discover -s tests   # 24 tests
python3 examples/demo.py                # per-round stats + resume comparison
```

The suite covers: two prefixes that agree now but need a length-4 suffix to
distinguish, empty-string differences, dead states, alphabet-order
tie-breaking, budget stops with unknown cells, verified/rejected external
counterexamples, checkpoint binding (alphabet / `L` / oracle version), and
resume at arbitrary query points — where fresh and resumed runs must produce
identical per-round records and domain behaviour.  The product-graph oracle
is cross-checked against the independent brute-force enumerator on random
machines, and every final hypothesis is verified by full enumeration of the
(small) domain.
