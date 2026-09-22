#!/usr/bin/env python3
"""Counterexample 1: naive trie deletion of a prefix word deletes longer words.

Naive strategy: to delete "app", walk to the terminal node and prune it (and
the now-childless path above it).  Because "app" is a prefix of "apple", the
prune removes the subtree that "apple" depends on.

The minimal automaton in mdawg.py instead clears only the final flag when the
terminal state still has children, so longer words survive.

Run:  python3 counterexamples/ce1_prefix_delete.py
Exit code 0 iff the naive approach fails as predicted AND mdawg is correct.
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from mdawg import MinimalAcyclicAutomaton


class NaiveTrie:
    def __init__(self):
        self.root = {}

    def add(self, word):
        node = self.root
        for ch in word:
            node = node.setdefault(ch, {})
        node["$"] = True

    def contains(self, word):
        node = self.root
        for ch in word:
            if ch not in node:
                return False
            node = node[ch]
        return node.get("$", False)

    def delete(self, word):
        # BUG: deletes the whole subtree rooted at the word's terminal node,
        # which also erases every longer word passing through it.
        node = self.root
        for ch in word[:-1]:
            if ch not in node:
                return
            node = node[ch]
        node.pop(word[-1], None)


def main():
    trie = NaiveTrie()
    for w in ("app", "apple"):
        trie.add(w)
    trie.delete("app")
    naive_loses_apple = not trie.contains("apple")
    print(f"naive trie:  'app' deleted -> 'apple' present? {trie.contains('apple')}")

    automaton = MinimalAcyclicAutomaton("aelps")
    for w in ("app", "apple"):
        automaton.add(w)
    automaton.remove("app")
    mdawg_keeps_apple = "apple" in automaton and "app" not in automaton
    print(f"mdawg:       'app' deleted -> 'apple' present? {'apple' in automaton}, "
          f"'app' present? {'app' in automaton}")

    assert naive_loses_apple, "expected the naive strategy to lose 'apple'"
    assert mdawg_keeps_apple, "mdawg must keep 'apple' and drop 'app'"
    assert automaton.is_minimal()
    print("OK: naive prefix-delete is broken; mdawg handles it correctly.")


if __name__ == "__main__":
    main()
