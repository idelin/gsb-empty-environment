#!/usr/bin/env python3
"""Sample session: online add/delete on a minimal acyclic dictionary automaton.

Run:  python3 demo.py
"""

from mdawg import Lexicon

lex = Lexicon(alphabet=set("aelpstr"))
print("alphabet:", "".join(sorted(lex.automaton.alphabet)))

print("\n-- add words (repeated adds count) --")
lex.add("app", count=3)
lex.add("apple")
lex.add("apples")
lex.add("ape", count=2)
lex.add("pet")
print("words:", lex.words_with_prefix())
print("reachable states:", lex.automaton.reachable_states(),
      "| minimal:", lex.automaton.is_minimal())

print("\n-- prefix queries (metadata joined from the separate store) --")
print("has_prefix('appl'):", lex.has_prefix("appl"))
print("words_with_prefix('app'):", lex.words_with_prefix("app"))

print("\n-- delete a prefix word; longer words survive --")
lex.remove("app")          # count 3 -> 2
lex.remove("app")
lex.remove("app")          # count 0 -> leaves the automaton
print("'app' in lexicon:", "app" in lex, "| 'apple' in lexicon:", "apple" in lex)
print("words:", lex.words_with_prefix())

print("\n-- shared suffix copy-on-write --")
lex.add("ape")             # count 2 -> 3
lex.add("pet", count=4)
before = lex.automaton.reachable_states()
lex.remove("pet")          # 4 -> 3, structure untouched
print("'pet' count:", lex.frequency("pet"), "| states still:", before == lex.automaton.reachable_states())
while lex.remove("pet"):
    pass
print("after removing all 'pet':", lex.words_with_prefix())
print("minimal:", lex.automaton.is_minimal(),
      "| reachable:", lex.automaton.reachable_states(),
      "| allocated:", lex.automaton.total_states())

print("\n-- compaction (proven rebuild strategy) --")
compact = lex.automaton.compact()
print("after compact: allocated:", compact.total_states(),
      "| language preserved:", sorted(compact.iter_words()) == [w for w, _ in lex.words_with_prefix()])
