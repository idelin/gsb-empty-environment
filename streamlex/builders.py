"""Small helpers that build explicit DFAs for common shapes.

These only assemble explicit transition tables -- no regular
expressions or automaton libraries are involved.
"""
from __future__ import annotations

from .dfa import DFA


def literal(word: str) -> DFA:
    """DFA matching exactly ``word`` (a chain of states)."""
    if not word:
        raise ValueError("literal() needs a non-empty word")
    transitions = {(i, ch): i + 1 for i, ch in enumerate(word)}
    return DFA(
        states=set(range(len(word) + 1)),
        alphabet=set(word),
        start=0,
        transitions=transitions,
        accepts={len(word)},
    )


def char_class_plus(chars) -> DFA:
    """DFA matching one or more characters from ``chars``."""
    chars = set(chars)
    if not chars:
        raise ValueError("char_class_plus() needs a non-empty set")
    transitions = {(0, c): 1 for c in chars}
    transitions.update({(1, c): 1 for c in chars})
    return DFA(states={0, 1}, alphabet=chars, start=0, transitions=transitions, accepts={1})


def star_then_literal(prefix_chars, word: str) -> DFA:
    """DFA matching ``prefix_chars*`` followed by exactly ``word``.

    Useful to build rules with arbitrarily long *failing prefixes*:
    the input can grow without ever accepting until ``word`` arrives.
    """
    prefix_chars = set(prefix_chars)
    if not word:
        raise ValueError("star_then_literal() needs a non-empty word")
    if prefix_chars & set(word):
        raise ValueError("prefix chars and word must be disjoint for a simple DFA")
    states = set(range(len(word) + 1))
    transitions = {(0, c): 0 for c in prefix_chars}
    for i, ch in enumerate(word):
        transitions[(i, ch)] = i + 1
    return DFA(
        states=states,
        alphabet=prefix_chars | set(word),
        start=0,
        transitions=transitions,
        accepts={len(word)},
    )
