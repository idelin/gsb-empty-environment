# Symbolic Automata (interval alphabets, Python 3.11, stdlib only)

Finite automata whose edges are labelled with **integer intervals or
character classes**. All set operations work on **alphabet partitions**
(disjoint cells), never by enumerating the alphabet — alphabets like
`[0, 2^40]` cost the same as `{0,1}`. No regex/automata libraries are used;
the alphabet and transition structure are given explicitly.

## Layout

- `automata/intervals.py` — `Interval`, `char_interval`, `partition` (endpoint sweep)
- `automata/nfa.py` — epsilon-`NFA`, epsilon closure (cycle-safe), subset step
  with per-step original-edge evidence, `simulate` (independent interpreter),
  JSON serialization
- `automata/lazy.py` — lazy views: `SubsetConstruction` (on-demand
  determinization, missing edges complete to the dead state), `Product`
  (`inter`/`union`/`diff`, partitions unified first), `Complement`
- `automata/search.py` — `EmptinessSearch`: pausable/resumable BFS for the
  **shortest** accepted word; state-budget exhaustion yields `UNKNOWN`
  (undecided), `resume(state_budget=...)` continues where it stopped
- `automata/inclusion.py` — `check_inclusion(A, B)` = emptiness of `A \ B`;
  `InclusionCache` keyed by automaton identity **and version** — editing one
  edge bumps the version and locally invalidates derived caches, so stale
  verdicts are never reused
- `automata/verify.py` — standalone counterexample verifier using only the
  independent interpreter: `python3.11 -m automata.verify case.json`

## Key properties

- Two automata with inconsistent alphabet boundaries are composed over a
  **unified partition** (`unified_cells`).
- Counterexamples carry **per-step evidence**: the partition cell, the chosen
  symbol, and the original NFA edges that fired (empty on the rejecting side
  = dead-state evidence). The empty word can be a counterexample.
- Every reported counterexample is re-checked by the independent NFA
  interpreter (`NFA.simulate`) before being returned (`verified=True`), and
  can be re-executed standalone from its JSON artifact.

## Run

```sh
python3.11 -m unittest discover -s tests -t . -v   # 19 tests
python3.11 examples/replay.py                       # 8 replayable scenarios
python3.11 -m automata.verify examples/out/counterexample.json
```
