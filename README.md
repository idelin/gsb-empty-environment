# dfacmp

Go library that decides whether two deterministic finite automata accept the
same language.

## Core comparison

`Compare(a, b)` explores the product graph in (length, lexicographic) word
order, so the first distinguishing word found is the shortest and, among the
shortest, the lexicographically smallest.

- Missing transitions fall into the implicit rejecting `Sink` state.
- The empty word is checked first (the search starts at the start-state pair).
- A counterexample result carries the distinguishing `Word`, its
  product-state `Path` (`len(Path) == len(Word)+1`), and `AcceptedBy`.

## Partial automata with external fragments

A `State` may carry an unexpanded `Fragment`; such a state is opaque — its
acceptance and transitions are unknown. The result `Kind` is one of:

- `Equivalent` — only ever reported once every fragment is expanded and the
  full product graph has been explored;
- `Counterexample` — with word, path, and accepting side;
- `Unknown` — with `Blocking`, the ids of the unexpanded fragments.

## Async expansion, versioning, incrementality

`Engine` wraps two automata with an `Expander`. `Engine.Query()` binds the
current automaton versions, works on a private snapshot (old queries stay
bound to old versions), expands blocking fragments asynchronously, and then
continues the same search incrementally via `Search.Resume` — already
visited product states are never re-explored. Successful expansions are also
applied to the live automata so later queries start further along. Decided
results are cached by version pair and re-validated with `FullProductCheck`
before being served.

## Verification helpers

- `VerifyCounterexample` — the word is accepted by exactly one side and the
  product-state path is consistent.
- `EnumerateCheck` — brute-force enumeration of short words cross-checks the
  result (minimality of counterexamples, absence of differences for
  equivalence claims).
- `FullProductCheck` — requires full expansion, re-runs the complete product
  search, and confirms cached results and counterexample minimality.
