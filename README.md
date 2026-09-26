# Symbolic ε-NFA Inclusion Toolkit

Pure Python 3.11 (standard library only). Automaton edges carry **integer
intervals / character classes** (`SymbolSet`); all set operations and the
alphabet partitioning work on interval boundaries, so huge alphabets
(e.g. `0 .. 2**32-1`) are never enumerated. Alphabets and transition
structures are supplied explicitly — no regex or automata libraries.

## Layout

- `automata/symbols.py` — `Interval`, `SymbolSet` (union / intersect /
  difference / complement), boundary `partition`, `refine_atoms` for
  unifying two alphabets with inconsistent boundaries.
- `automata/nfa.py` — explicit `NFA` (interval edges + ε-edges), version /
  dirty-state tracking on every mutation, and `accepts()`: the
  **independent interpreter** used to re-check counterexamples.
- `automata/determinize.py` — `LazyDFA`: on-demand subset construction
  with dependency-tracked caching; editing an edge drops only the cache
  entries derived from the touched states.
- `automata/inclusion.py` — `InclusionSearch`: BFS over the on-demand
  product graph (shortest distinguishing string, per-step original-edge
  evidence, missing transitions fall into the implicit dead state, state
  budget → `UNDECIDED`, `step()`/`snapshot()`/`restore()` for pause &
  resume). `InclusionChecker` owns the caches, invalidates them locally
  after edge edits, and never reuses a stale inclusion conclusion.
- `automata/serialize.py` / `automata/verify.py` — JSON export of NFAs and
  counterexamples; standalone verifier (`python -m automata.verify`).
- `automata/samples.py` — nine replayable end-to-end samples.
- `tests/` — 44 `unittest` cases.

## Quick start

```python
from automata import NFA, SymbolSet, InclusionChecker, Status

uni = SymbolSet.range(0, 127)
a = NFA(uni, 3, 0, finals={2})
a.add_edge(0, SymbolSet.chars("a"), 1)
a.add_edge(1, SymbolSet.char_range("0", "9"), 2)
b = NFA(uni, 2, 0, finals={1})
b.add_edge(0, SymbolSet.chars("a"), 1)

result = InclusionChecker(a, b).check()
assert result.status is Status.NOT_INCLUDED
result.counterexample.word          # (97, 48)  == "a0", shortest
result.counterexample.steps[0]      # symbol, partition atom, original edges of A and B
```

## Replayable samples

```
$ python3.11 -m automata.samples
[1] overlapping char classes: ident⊆lower -> not_included, cex='A' (verified); lower⊆ident -> included
[2] epsilon loops: eps-loop≡plain, both inclusions included/included
[3] large intervals (|Σ|=2^32): A⊆B -> included; B⊆A -> not_included, cex=[524288] (verified)
[4] empty string: cex=(empty string) (verified)
[5] dead state: cex='ab', B-side dies (edges_b at last step: 0) (verified)
[6] unified alphabets: A⊆B -> included; B⊆A -> not_included, cex=[201] (verified)
[7] exponential blowup: n=11 NFA states -> 1024 determinized states; budget=100 -> undecided, resumed -> included after 1024 expansions
[8] pause/resume: snapshot after 2 expansions, JSON round-trip, resumed -> not_included, cex=[6, 0, 0] == direct run (verified)
[9] cache invalidation: INCLUDED -> edit B edge 1-b->2 to 1-c->2 -> not_included, cex='ab' (verified); product cache 4 entries, 1 locally invalidated, 3 survived
```

## Independently executable counterexamples

A counterexample plus the two NFA definitions serialize to plain JSON and
can be re-checked without any of the search machinery — the verifier
re-interprets the word on the original NFAs and replays every evidence
edge:

```
$ python3.11 -m automata.verify a.json b.json cex.json
OK: word [97, 48] is accepted by A and rejected by B; all 2 evidence steps check out.
```

## Tests

```
$ python3.11 -m unittest discover -s tests
Ran 44 tests in 0.13s
OK
```

Coverage includes: interval algebra and boundary partitioning, overlapping
character classes, ε-loops, huge alphabets, empty-string counterexamples,
implicit dead states, alphabet-boundary unification, shortest
distinguishing strings with per-step edge evidence, budget → `UNDECIDED`
with resume, pause/snapshot/JSON-restore equivalence, exponential
determinization blowup (2^(n-1) subsets), local cache invalidation after a
single edge edit (stale conclusions never reused), and tampered-counterexample
rejection by the independent verifier.
