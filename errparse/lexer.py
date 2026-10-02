"""Maximal-munch tokenizer driven by an explicitly supplied DFA.

No regex or automata libraries: the alphabet, transition table, start
state and accepting states are plain explicit inputs.
"""
from __future__ import annotations

from dataclasses import dataclass

from .tokens import eof_token, source_token


@dataclass(frozen=True)
class DFA:
    alphabet: frozenset
    transitions: dict  # (state, char) -> state
    start: str
    accepts: dict  # state -> token type


class LexError(Exception):
    def __init__(self, pos, char):
        self.pos = pos
        self.char = char
        super().__init__(f"unexpected character {char!r} at offset {pos}")


def tokenize(dfa, text, keywords=None, skip=" \t\n"):
    keywords = keywords or {}
    tokens = []
    i = 0
    n = len(text)
    while i < n:
        c = text[i]
        if c in skip:
            i += 1
            continue
        if c not in dfa.alphabet:
            raise LexError(i, c)
        state = dfa.start
        last_accept = None
        last_end = i
        j = i
        while j < n and (state, text[j]) in dfa.transitions:
            state = dfa.transitions[(state, text[j])]
            j += 1
            if state in dfa.accepts:
                last_accept = state
                last_end = j
        if last_accept is None:
            raise LexError(i, text[i])
        value = text[i:last_end]
        ttype = dfa.accepts[last_accept]
        ttype = keywords.get(value, ttype)
        tokens.append(source_token(ttype, value, i))
        i = last_end
    tokens.append(eof_token(n))
    return tokens
