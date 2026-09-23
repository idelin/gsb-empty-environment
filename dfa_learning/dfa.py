"""Explicit deterministic finite automaton.

The same class is used for the hidden machine (held only by the test side /
oracles) and for the executable hypothesis produced by the learner.  The
learner itself never sees an instance of the hidden machine.
"""

from __future__ import annotations


class DFA:
    """A complete deterministic finite automaton.

    Words are plain strings; each symbol is a single character.  The
    transition function must be total over ``states x alphabet``.
    """

    def __init__(self, states, alphabet, start, transitions, accepting):
        self.states = frozenset(states)
        self.alphabet = tuple(alphabet)
        self.start = start
        self.transitions = dict(transitions)
        self.accepting = frozenset(accepting)
        self._validate()

    def _validate(self):
        if self.start not in self.states:
            raise ValueError("start state is not in the state set")
        if not self.accepting <= self.states:
            raise ValueError("accepting set references unknown states")
        if len(set(self.alphabet)) != len(self.alphabet):
            raise ValueError("alphabet contains duplicate symbols")
        for state in self.states:
            for symbol in self.alphabet:
                target = self.transitions.get((state, symbol))
                if target not in self.states:
                    raise ValueError(
                        f"missing or invalid transition from {state!r} on {symbol!r}"
                    )

    @property
    def num_states(self):
        return len(self.states)

    def step(self, state, symbol):
        return self.transitions[(state, symbol)]

    def run(self, word):
        state = self.start
        for symbol in word:
            state = self.step(state, symbol)
        return state

    def accepts(self, word):
        return self.run(word) in self.accepting

    def is_accepting(self, state):
        return state in self.accepting
