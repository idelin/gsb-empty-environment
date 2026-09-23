"""Deterministic finite automaton.

Used both as the hidden machine held by the test side and as the
executable hypothesis produced by the learner.  Transition functions are
total (complete DFAs) so any word over the alphabet can be executed.
"""
from __future__ import annotations

import hashlib
import json


class DFA:
    def __init__(self, num_states, alphabet, start, transitions, accepting):
        self.alphabet = tuple(alphabet)
        if not self.alphabet:
            raise ValueError("alphabet must be non-empty")
        if len(set(self.alphabet)) != len(self.alphabet):
            raise ValueError("alphabet symbols must be distinct")
        for a in self.alphabet:
            if not isinstance(a, str) or len(a) != 1:
                raise ValueError("alphabet symbols must be single characters")
        if not 0 <= start < num_states:
            raise ValueError("start state out of range")
        self.num_states = num_states
        self.start = start
        self.transitions = {}
        for (q, a), t in transitions.items():
            if not (0 <= q < num_states and 0 <= t < num_states):
                raise ValueError("transition endpoint out of range")
            if a not in self.alphabet:
                raise ValueError(f"transition over unknown symbol {a!r}")
            self.transitions[(q, a)] = t
        for q in range(num_states):
            for a in self.alphabet:
                if (q, a) not in self.transitions:
                    raise ValueError(f"incomplete DFA: missing delta({q}, {a!r})")
        self.accepting = frozenset(accepting)
        for q in self.accepting:
            if not 0 <= q < num_states:
                raise ValueError("accepting state out of range")

    def step(self, state, symbol):
        return self.transitions[(state, symbol)]

    def run(self, word):
        q = self.start
        for a in word:
            if a not in self.alphabet:
                raise ValueError(f"symbol {a!r} not in alphabet")
            q = self.step(q, a)
        return q

    def accepts(self, word):
        return self.run(word) in self.accepting

    def signature(self):
        """Stable fingerprint, used as an oracle-version binding."""
        payload = {
            "num_states": self.num_states,
            "alphabet": list(self.alphabet),
            "start": self.start,
            "transitions": sorted([q, a, t] for (q, a), t in self.transitions.items()),
            "accepting": sorted(self.accepting),
        }
        blob = json.dumps(payload, sort_keys=True).encode("utf-8")
        return hashlib.sha256(blob).hexdigest()[:16]

    def __repr__(self):
        return (f"DFA(states={self.num_states}, start={self.start}, "
                f"accepting={sorted(self.accepting)})")
