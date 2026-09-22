#!/usr/bin/env python3
"""Counterexample 3: word-frequency metadata cannot live inside automaton states.

In a minimal automaton the final leaf state (final=True, no transitions) is
shared by EVERY word that ends a path, e.g. "bad" and "dad" both terminate in
the same state.  A counter stored on that state counts all of them at once,
so per-word frequencies are unrecoverable.  mdawg.Lexicon therefore keeps
frequencies in a dict outside the automaton.

Run:  python3 counterexamples/ce3_frequency_in_states.py
Exit code 0 iff state-embedded counts fail as predicted AND Lexicon is correct.
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from mdawg import Lexicon, MinimalAcyclicAutomaton


def main():
    automaton = MinimalAcyclicAutomaton("abd")
    automaton.add("bad")
    automaton.add("dad")

    # Prove the two words share their terminal state.
    def terminal(word):
        node = automaton._root
        for ch in word:
            node = automaton._trans[node][ch]
        return node

    shared_terminal = terminal("bad") == terminal("dad")
    print(f"'bad' and 'dad' share terminal state? {shared_terminal}")

    # A counter on the shared terminal state cannot distinguish the words.
    state_counter = {terminal("bad"): 0}
    for word, n in (("bad", 7), ("dad", 2)):
        state_counter[terminal(word)] += n
    embedded_count_is_wrong = state_counter[terminal("bad")] == 9
    print(f"state-embedded counter for 'bad' reads {state_counter[terminal('bad')]} "
          f"(expected 7)")

    lex = Lexicon("abd")
    lex.add("bad", count=7)
    lex.add("dad", count=2)
    separated_ok = lex.frequency("bad") == 7 and lex.frequency("dad") == 2
    print(f"Lexicon: frequency('bad')={lex.frequency('bad')}, "
          f"frequency('dad')={lex.frequency('dad')}")

    assert shared_terminal, "expected a shared terminal state"
    assert embedded_count_is_wrong, "expected embedded counter to be wrong"
    assert separated_ok, "Lexicon must keep per-word counts correct"
    print("OK: frequencies belong outside the automaton; Lexicon separates them.")


if __name__ == "__main__":
    main()
