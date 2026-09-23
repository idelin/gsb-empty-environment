# query-learn-dfa

Experimental library for learning an unknown deterministic finite
automaton from queries.  Python 3.11+, standard library only, no
automata-learning packages.

## Model

- The **learner** may only ask *membership queries* and *bounded
  equivalence queries*.  It never sees the hidden machine's states or
  transition table.
- The **test side** holds the hidden DFA and exposes it as oracles.
- The alphabet is finite; the query domain is all words of length
  `<= L`.

## Modules

- `dfa.py` — complete, executable DFA (hidden machine and hypothesis),
  with a structural `signature()` used as the oracle version.
- `oracle.py` — `MembershipOracle` and `BoundedEquivalenceOracle`.
  The equivalence oracle runs a best-first search on the product graph
  of hidden machine and hypothesis, keyed by `(length, word-rank)`, so
  it returns a shortest counterexample and, among shortest ones, the
  lexicographically smallest *with respect to the alphabet order*.  It
  never enumerates the exponentially many words one by one.
- `learner.py` — L*-style observation-table learner:
  - table kept closed and consistent; hypothesis built from distinct
    rows of `S`;
  - every cell resolved by a real membership query (cache-backed);
    undetermined cells raise instead of being assumed `False`;
  - counterexamples processed by enqueueing their distinguishing
    suffixes into `E`;
  - total membership-query budget; on exhaustion the learner saves the
    table, the pending-suffix queue (counterexample processing
    position), the cache and all counters, and resumes later with
    identical behaviour;
  - checkpoints bind alphabet, `L` and oracle version; a mismatch
    raises `CheckpointMismatch` and old evidence is refused;
  - external counterexamples are verified with a membership query
    first; false ones are rejected without touching the table;
  - results distinguish `equivalent_in_domain`, `budget_undecided`
    (and record every counterexample found) — in-domain equivalence
    never claims the true language is identified at all lengths.
- `verifier.py` — independent full-word enumerator (small `L` only)
  that cross-checks the product oracle's shortest counterexample and
  final in-domain equivalence (`BruteForceCheckedOracle`).
- `machines.py` — example hidden machines (suffix automaton, long-suffix
  chain with dead state, forbidden-substring machine, random DFAs).
- `run_experiment.py` — demo: per-round state counts, cache hits and
  new membership queries; fresh run vs. checkpoint/resume comparison.
- `test_learning.py` — `unittest` suite (17 tests).

## Usage

```bash
python3 run_experiment.py     # demo with per-round statistics
python3 -m unittest test_learning -v
```

```python
from learner import ObservationTableLearner
from oracle import BoundedEquivalenceOracle, MembershipOracle

mq = MembershipOracle(hidden)                       # test side
eq = BoundedEquivalenceOracle(hidden, alphabet, L)  # test side
learner = ObservationTableLearner(alphabet, L, mq, eq.version, budget=1000)
result = learner.learn(eq)          # learner side: queries only
if result.status == "budget_undecided":
    learner.save_checkpoint("ckpt.json")
    learner = ObservationTableLearner.resume_from_checkpoint(
        "ckpt.json", mq, eq.version, budget=500)
    result = learner.learn(eq)
```

## Test coverage

- product oracle vs. brute-force enumeration on random DFAs (small `L`);
- hidden states needing long distinguishing suffixes (chain machine);
- empty-string difference, dead states, alphabet-order sensitivity of
  the lexicographically smallest counterexample;
- external counterexample verification (false ones rejected, table
  unpolluted);
- checkpoint binding mismatches (alphabet / `L` / oracle version);
- budget exhaustion at arbitrary query points: resumed runs gather
  exactly the same membership evidence and the final hypothesis behaves
  identically to a fresh run on the whole domain;
- in-domain equivalence is never reported as identification of the
  true language beyond `L`.
