"""Hand-rolled finite automata over an explicitly supplied alphabet.

No regular-expression or automata library is used anywhere in this package:
the alphabet and the transition relation are always explicit inputs, NFA
simulation and subset construction are implemented directly here.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass

from .tokens import Token, eof_token

EPSILON_STEP = None  # transition label for epsilon moves


class AutomatonError(ValueError):
    pass


class LexError(Exception):
    pass


@dataclass
class NFA:
    alphabet: frozenset  # explicit input alphabet
    states: frozenset
    transitions: dict  # (state, symbol | None) -> frozenset of states
    start: object
    accepts: frozenset

    @classmethod
    def from_transitions(cls, alphabet, transitions, start, accepts) -> "NFA":
        alphabet = frozenset(alphabet)
        states = {start} | set(accepts)
        norm: dict = {}
        for (src, sym), targets in transitions.items():
            if sym is not None and sym not in alphabet:
                raise AutomatonError(f"symbol {sym!r} is not in the alphabet")
            targets = frozenset(targets)
            states.add(src)
            states |= set(targets)
            key = (src, sym)
            norm[key] = norm.get(key, frozenset()) | targets
        return cls(
            alphabet=alphabet,
            states=frozenset(states),
            transitions=norm,
            start=start,
            accepts=frozenset(accepts),
        )

    def epsilon_closure(self, states) -> frozenset:
        closure = set(states)
        work = list(states)
        while work:
            state = work.pop()
            for target in self.transitions.get((state, EPSILON_STEP), ()):
                if target not in closure:
                    closure.add(target)
                    work.append(target)
        return frozenset(closure)

    def step(self, states, symbol) -> frozenset:
        moved: set = set()
        for state in states:
            moved |= set(self.transitions.get((state, symbol), ()))
        return self.epsilon_closure(moved) if moved else frozenset()

    def accepts_string(self, text: str) -> bool:
        current = self.epsilon_closure({self.start})
        for ch in text:
            if ch not in self.alphabet:
                return False
            current = self.step(current, ch)
            if not current:
                return False
        return bool(current & self.accepts)

    def to_dfa(self) -> "DFA":
        start_set = self.epsilon_closure({self.start})
        names = {start_set: 0}
        transitions: dict = {}
        accepts: set = set()
        work = [start_set]
        while work:
            current = work.pop()
            current_id = names[current]
            if current & self.accepts:
                accepts.add(current_id)
            for symbol in sorted(self.alphabet):
                nxt = self.step(current, symbol)
                if not nxt:
                    continue
                if nxt not in names:
                    names[nxt] = len(names)
                    work.append(nxt)
                transitions[(current_id, symbol)] = names[nxt]
        return DFA(
            alphabet=self.alphabet,
            transitions=transitions,
            start=0,
            accepts=frozenset(accepts),
        )


@dataclass
class DFA:
    alphabet: frozenset
    transitions: dict  # (state, symbol) -> state
    start: object
    accepts: frozenset

    def accepts_string(self, text: str) -> bool:
        state = self.start
        for ch in text:
            state = self.transitions.get((state, ch))
            if state is None:
                return False
        return state in self.accepts

    def longest_prefix(self, text: str, pos: int = 0) -> int:
        """Length of the longest accepted prefix of text[pos:]."""
        state = self.start
        best = 0
        length = 0
        if state in self.accepts:
            best = 0
        while pos + length < len(text):
            state = self.transitions.get((state, text[pos + length]))
            if state is None:
                break
            length += 1
            if state in self.accepts:
                best = length
        return best


_state_counter = itertools.count()


def _fresh() -> str:
    return f"q{next(_state_counter)}"


def _check_alphabets(nfas) -> frozenset:
    alphabet = nfas[0].alphabet
    for nfa in nfas:
        if nfa.alphabet != alphabet:
            raise AutomatonError("cannot combine NFAs over different alphabets")
    return alphabet


def literal(word: str, alphabet) -> NFA:
    """NFA accepting exactly ``word``; alphabet given explicitly."""
    alphabet = frozenset(alphabet)
    missing = set(word) - alphabet
    if missing:
        raise AutomatonError(f"literal uses symbols outside the alphabet: {missing}")
    states = [_fresh() for _ in range(len(word) + 1)]
    transitions = {
        (states[i], ch): {states[i + 1]} for i, ch in enumerate(word)
    }
    return NFA.from_transitions(alphabet, transitions, states[0], {states[-1]})


def symbol(chars, alphabet) -> NFA:
    """NFA accepting a single character from ``chars``."""
    alphabet = frozenset(alphabet)
    missing = set(chars) - alphabet
    if missing:
        raise AutomatonError(f"symbols outside the alphabet: {missing}")
    start, accept = _fresh(), _fresh()
    transitions = {(start, ch): {accept} for ch in chars}
    return NFA.from_transitions(alphabet, transitions, start, {accept})


def union(*nfas) -> NFA:
    alphabet = _check_alphabets(nfas)
    start = _fresh()
    transitions = {(start, EPSILON_STEP): {nfa.start for nfa in nfas}}
    accepts = set()
    for nfa in nfas:
        transitions.update(nfa.transitions)
        accepts |= set(nfa.accepts)
    return NFA.from_transitions(alphabet, transitions, start, accepts)


def concat(*nfas) -> NFA:
    alphabet = _check_alphabets(nfas)
    transitions: dict = {}
    for nfa in nfas:
        transitions.update(nfa.transitions)
    for left, right in zip(nfas, nfas[1:]):
        for accept in left.accepts:
            key = (accept, EPSILON_STEP)
            transitions[key] = set(transitions.get(key, set())) | {right.start}
    return NFA.from_transitions(
        alphabet, transitions, nfas[0].start, set(nfas[-1].accepts)
    )


def star(nfa: NFA) -> NFA:
    start = _fresh()
    transitions = dict(nfa.transitions)
    transitions[(start, EPSILON_STEP)] = {nfa.start}
    for accept in nfa.accepts:
        key = (accept, EPSILON_STEP)
        transitions[key] = set(transitions.get(key, set())) | {nfa.start}
    return NFA.from_transitions(
        nfa.alphabet, transitions, start, set(nfa.accepts) | {start}
    )


def plus(nfa: NFA) -> NFA:
    return concat(nfa, star(nfa))


@dataclass
class LexerSpec:
    kind: str
    nfa: NFA
    skip: bool = False


class Lexer:
    """Deterministic longest-match lexer over explicit automata."""

    def __init__(self, specs) -> None:
        self._specs = [
            (spec.kind, spec.nfa.to_dfa(), spec.skip) for spec in specs
        ]

    def tokenize(self, text: str) -> list[Token]:
        tokens: list[Token] = []
        pos = 0
        while pos < len(text):
            best_len = 0
            best_kind = None
            best_skip = False
            # longest match wins; ties keep the earliest spec
            for kind, dfa, skip in self._specs:
                matched = dfa.longest_prefix(text, pos)
                if matched > best_len:
                    best_len = matched
                    best_kind = kind
                    best_skip = skip
            if best_len == 0:
                raise LexError(f"no token matches at offset {pos}: {text[pos:]!r}")
            if not best_skip:
                tokens.append(
                    Token(
                        kind=best_kind,
                        text=text[pos : pos + best_len],
                        start=pos,
                        end=pos + best_len,
                    )
                )
            pos += best_len
        return tokens
