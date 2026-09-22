# Minimal Acyclic Dictionary Automaton (online add/delete)

Pure Python 3.11+, standard library only. No regex or automata libraries:
the alphabet and the transition structure are explicit constructor inputs
and plain data (`dict`/`tuple`), so the automaton is fully inspectable.

## Files

- `dawg.py` — `MinimalWordAutomaton`: online minimal acyclic DFA for a finite
  word set. Structural queries only (membership, prefix enumeration).
- `metadata.py` — `FrequencyTable`: word-frequency metadata, kept fully
  separate from the automaton.
- `dictionary.py` — `FrequencyDictionary`: facade joining the two; the
  automaton is touched only when a word's count crosses 0 ↔ 1.
- `counterexamples.py` — independently executable counterexamples
  (`python3 counterexamples.py`, exit code 0 on success).
- `test_dawg.py` — unittest suite (`python3 -m unittest test_dawg -v`).
- `demo.py` — runnable sample session.

## Design

**Explicit alphabet and transitions.** The automaton is constructed with an
explicit alphabet; every word is validated against it. A state is an
immutable `_Node(final, children)` where `children` is a sorted tuple of
`(char, _Node)` pairs — the transition function is plain data.

**Minimality by hash-consing.** All states are interned in a registry keyed
by `(final, children)`. Because children are canonicalized before their
parents (insertion/deletion rebuilds the path bottom-up), two states are the
same object iff they have the same finality and, for every symbol, transition
to the same states — i.e. iff their right languages are equal.

*Proof of minimality.* For an acyclic DFA, state equivalence (equal right
languages) satisfies: `p ~ q` iff `final(p) = final(q)` and for every symbol
`c`, `δ(p,c) ~ δ(q,c)` (both undefined counts as equal). By induction on the
height of a state (longest path to a sink): height-0 states are childless, so
exactly two equivalence classes exist (final / non-final), and the registry
holds exactly one node for each. For height `h > 0`, all children have height
`< h` and are canonical by the induction hypothesis, so the key
`(final, children)` is equal for two states iff they are equivalent; interning
therefore keeps exactly one representative per class. Hence every reachable
state is the unique representative of its equivalence class, which is exactly
the minimal DFA (Myhill–Nerode) for the stored language. ∎

*Language invariant.* `add`/`remove` flip exactly the finality of the state
reached by the target word and add/remove exactly the edges on its path, so
the accepted language is always exactly the current word set. The randomized
test `test_language_matches_reference_set_randomized` cross-checks 2000
mutations against a Python `set`.

**Copy-on-write.** Nodes are immutable. A mutation allocates new nodes only
along the root-to-target path (O(word length)); every other path keeps
sharing the untouched subtrees, so editing one word can never pollute
another — including words that share minimized suffix states.
`snapshot()` is O(1) and persistent: later mutations cannot affect it.

**Deletion semantics.** Deleting a word only clears the final flag of its
state and prunes edges that lead to dead (non-final, childless) leaves.
Deleting `"abc"` therefore never affects `"abcd"`. Duplicate words are
counted in `FrequencyTable`; a word leaves the automaton only when its count
reaches 0.

**Metadata separation.** The automaton stores no frequencies; prefix queries
(`words_with_prefix`) run purely on structure. Frequencies live in
`FrequencyTable` and are joined on demand (`frequencies_with_prefix`).

**Rebuild/GC note.** The registry may retain nodes that are no longer
reachable from the root (`registry_size` ≥ `state_count`). Since nodes are
immutable and shared, this is harmless for correctness; a stop-and-copy GC
(re-interning all states reachable from the root into a fresh registry)
reclaims them in O(number of live states) and preserves minimality, because
re-interning canonical nodes yields the same canonical nodes.

## Sample run

```
$ python3 demo.py
words with prefix 'he': ['he', 'hen', 'her', 'here']
frequencies under 'he': [('he', 1), ('hen', 2), ('her', 1), ('here', 1)]
count('hen') = 2
states: 7

after remove('he'):
contains('he') = False
contains('hen') = True  contains('here') = True

after remove('hen') once (was added twice):
contains('hen') = True  count = 1

snapshot words: ['hen', 'her', 'here', 'she']
current words: ['hen', 'she']
```

## Counterexamples

```
$ python3 counterexamples.py
[PASS] delete prefix word keeps longer words
[PASS] shared suffix copy-on-write
[PASS] duplicate adds counted, not set-like
[PASS] snapshot survives later mutations
[PASS] minimality: suffix states merged
5/5 counterexample checks passed
```

Each case reproduces the failure on a deliberately naive implementation
(subtree-dropping trie delete, in-place mutation of a shared suffix state)
and shows the minimal automaton surviving the same scenario.

## Test results

```
$ python3 -m unittest test_dawg -v
...
Ran 22 tests in 0.074s

OK
```

Covers: alphabet validation, add/contains, empty word, duplicate-add
counting, delete-prefix-keeps-longer (both directions), prefix queries,
minimality state counts (`{"a","ab","abc"}` → 4, `{"abc","xbc"}` → 4,
`{"dog","dogs","log","logs"}` → 5), copy-on-write on shared suffixes,
persistent snapshots, metadata/automaton separation, and a randomized
differential test against a reference set.
