"""Hidden machines held by the test side (never handed to the learner)."""

from dfa_learning import DFA

ALPHABET = ("a", "b")


def long_suffix_machine():
    """Accepts exactly "aaaaa".

    The prefixes "a" and "b" lead to states that are both non-accepting and
    behave identically for every continuation shorter than 4 symbols; only
    the length-4 suffix "aaaa" distinguishes them.
    """
    # states: 0 = start, 1..5 = A-chain (5 accepting), 6..10 = B-chain,
    # 11 = dead
    transitions = {}
    for state in range(12):
        for symbol in ALPHABET:
            transitions[(state, symbol)] = 11
    transitions[(0, "a")] = 1
    transitions[(0, "b")] = 6
    for i in range(1, 5):
        transitions[(i, "a")] = i + 1
    for i in range(6, 10):
        transitions[(i, "a")] = i + 1
    return DFA(range(12), ALPHABET, 0, transitions, accepting={5})


def empty_string_machine():
    """Accepts the empty word and every word ending in "b"."""
    # state 0: accepting (start / last symbol was b); state 1: last symbol was a
    transitions = {
        (0, "a"): 1, (0, "b"): 0,
        (1, "a"): 1, (1, "b"): 0,
    }
    return DFA([0, 1], ALPHABET, 0, transitions, accepting={0})


def epsilon_only_machine():
    """Accepts only the empty word."""
    transitions = {
        (0, "a"): 1, (0, "b"): 1,
        (1, "a"): 1, (1, "b"): 1,
    }
    return DFA([0, 1], ALPHABET, 0, transitions, accepting={0})


def dead_state_machine():
    """Accepts words without "bb"; state 2 is a non-accepting dead sink."""
    transitions = {
        (0, "a"): 0, (0, "b"): 1,
        (1, "a"): 0, (1, "b"): 2,
        (2, "a"): 2, (2, "b"): 2,
    }
    return DFA([0, 1, 2], ALPHABET, 0, transitions, accepting={0, 1})


def two_word_machine():
    """Accepts exactly {"ab", "ba"}; used to test alphabet-order tie-breaking."""
    # 0 start, 1 saw "a", 2 saw "b", 3 accepting, 4 dead
    transitions = {}
    for state in range(5):
        for symbol in ALPHABET:
            transitions[(state, symbol)] = 4
    transitions[(0, "a")] = 1
    transitions[(0, "b")] = 2
    transitions[(1, "b")] = 3
    transitions[(2, "a")] = 3
    return DFA(range(5), ALPHABET, 0, transitions, accepting={3})


def reject_all_machine():
    """One-state hypothesis that rejects everything (oracle test fixture)."""
    transitions = {(0, "a"): 0, (0, "b"): 0}
    return DFA([0], ALPHABET, 0, transitions, accepting=set())
