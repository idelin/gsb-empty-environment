"""Example hidden-machine builders used by the demo and the tests."""
from __future__ import annotations

from dfa import DFA


def ends_with_dfa(pattern, alphabet):
    """Accepts words that end with ``pattern`` (standard KMP-style automaton)."""
    k = len(pattern)
    transitions = {}
    for state in range(k + 1):
        for a in alphabet:
            s = pattern[:state] + a
            nxt = 0
            for length in range(min(k, len(s)), -1, -1):
                if s.endswith(pattern[:length]):
                    nxt = length
                    break
            transitions[(state, a)] = nxt
    return DFA(k + 1, alphabet, 0, transitions, {k})


def exact_a_run_dfa(n, alphabet=("a", "b")):
    """Accepts exactly the word "a"*n; everything else falls into a dead
    state.  States i < j are only distinguishable by suffixes of length
    at least n - j, so learning needs long distinguishing suffixes."""
    dead = n + 1
    transitions = {}
    for i in range(n + 1):
        transitions[(i, "a")] = i + 1 if i < n else dead
        transitions[(i, "b")] = dead
    transitions[(dead, "a")] = dead
    transitions[(dead, "b")] = dead
    return DFA(n + 2, alphabet, 0, transitions, {n})


def no_double_letter_dfa(letter, alphabet=("a", "b")):
    """Accepts words with no two consecutive ``letter``; has a dead state."""
    transitions = {}
    for a in alphabet:
        transitions[(0, a)] = 1 if a == letter else 0
        transitions[(1, a)] = 2 if a == letter else 0
        transitions[(2, a)] = 2
    return DFA(3, alphabet, 0, transitions, {0, 1})


def random_dfa(rng, num_states, alphabet):
    transitions = {(q, a): rng.randrange(num_states)
                   for q in range(num_states) for a in alphabet}
    accepting = {q for q in range(num_states) if rng.random() < 0.5}
    return DFA(num_states, alphabet, 0, transitions, accepting)
