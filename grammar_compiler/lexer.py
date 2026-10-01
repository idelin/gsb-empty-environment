"""Explicit-DFA lexer.

The alphabet and the transition structure are given explicitly as data; no
regular-expression or automata library is used anywhere.
"""
from __future__ import annotations


class DFA:
    """Deterministic finite automaton over an explicitly listed alphabet."""

    def __init__(self, alphabet, transitions, start, accepts):
        self.alphabet = frozenset(alphabet)
        self.transitions = dict(transitions)   # (state, char) -> state
        self.start = start
        self.accepts = frozenset(accepts)

    def longest_match(self, text, pos):
        """Return end index of the longest accepted prefix, or None."""
        state = self.start
        best = None
        i = pos
        while i < len(text):
            ch = text[i]
            if ch not in self.alphabet:
                break
            state = self.transitions.get((state, ch))
            if state is None:
                break
            i += 1
            if state in self.accepts:
                best = i
        return best


def literal_dfa(word):
    """Build a DFA recognising exactly one literal word."""
    transitions = {}
    for i, ch in enumerate(word):
        transitions[(i, ch)] = i + 1
    return DFA(set(word), transitions, 0, {len(word)})


class LexError(Exception):
    pass


class Lexer:
    def __init__(self, skip=" \t\n"):
        self.dfas = {}          # token name -> DFA
        self.skip = frozenset(skip)

    def add_token(self, name, dfa):
        self.dfas[name] = dfa

    def tokenize(self, text):
        out = []
        pos = 0
        while pos < len(text):
            if text[pos] in self.skip:
                pos += 1
                continue
            best_name = None
            best_end = -1
            for name, dfa in self.dfas.items():
                end = dfa.longest_match(text, pos)
                if end is not None and end > best_end:
                    best_name, best_end = name, end
            if best_name is None:
                raise LexError(
                    f"no token matches at position {pos}: "
                    f"{text[pos:pos + 10]!r}")
            out.append(best_name)
            pos = best_end
        return out
