#!/usr/bin/env python3
"""Counterexample 2: in-place update of a shared suffix state pollutes other words.

In a minimal automaton, "dog" and "log" converge on the SAME state after the
first symbol (both right-languages are {"g"}... here the "og" suffix states).
A delete implemented by mutating states in place corrupts every word routed
through the shared state.  Copy-on-write (mdawg) never mutates existing
states, so "log" is unaffected by deleting "dog".

Run:  python3 counterexamples/ce2_shared_suffix_mutation.py
Exit code 0 iff in-place mutation fails as predicted AND mdawg is correct.
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from mdawg import MinimalAcyclicAutomaton


class MutatingAutomaton:
    """Minimal-style automaton that shares suffixes but mutates in place."""

    def __init__(self):
        self.states = [{"final": False, "trans": {}}]  # shared sink-ish states
        self.root = 0

    def _state_for(self, final, trans):
        for i, s in enumerate(self.states):
            if s["final"] == final and s["trans"] == trans:
                return i
        self.states.append({"final": final, "trans": dict(trans)})
        return len(self.states) - 1

    def add(self, word):
        # Build chain bottom-up with suffix sharing, then graft onto root.
        child = self._state_for(True, {})
        for ch in reversed(word[1:]):
            child = self._state_for(False, {ch: child})
        self.states[self.root]["trans"][word[0]] = child

    def contains(self, word):
        node = self.root
        for ch in word:
            node = self.states[node]["trans"].get(ch)
            if node is None:
                return False
        return self.states[node]["final"]

    def delete_in_place(self, word):
        # BUG: mutate the shared states along the path.
        node = self.root
        for ch in word:
            node = self.states[node]["trans"].get(ch)
            if node is None:
                return
        self.states[node]["final"] = False
        if not self.states[node]["trans"]:
            # prune the transition from the parent by mutating it in place
            parent = self.root
            for ch in word[:-1]:
                parent = self.states[parent]["trans"][ch]
            del self.states[parent]["trans"][word[-1]]


def main():
    bad = MutatingAutomaton()
    bad.add("dog")
    bad.add("log")
    # The "og" suffix states are shared between both words.
    bad.delete_in_place("dog")
    naive_pollutes = not bad.contains("log")
    print(f"in-place:    'dog' deleted -> 'log' present? {bad.contains('log')}")

    automaton = MinimalAcyclicAutomaton("dglo")
    automaton.add("dog")
    automaton.add("log")
    automaton.remove("dog")
    mdawg_safe = "log" in automaton and "dog" not in automaton
    print(f"mdawg (COW): 'dog' deleted -> 'log' present? {'log' in automaton}, "
          f"'dog' present? {'dog' in automaton}")

    assert naive_pollutes, "expected in-place mutation to pollute 'log'"
    assert mdawg_safe, "mdawg copy-on-write must protect shared suffixes"
    assert automaton.is_minimal()
    print("OK: in-place mutation pollutes shared suffixes; mdawg COW is safe.")


if __name__ == "__main__":
    main()
