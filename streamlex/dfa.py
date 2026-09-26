"""Explicitly specified deterministic finite automata.

No regular-expression or automaton library is used anywhere in this
package: the alphabet, the state set and the transition table are given
explicitly by the caller and validated at construction time.
"""
from __future__ import annotations


class DFAError(ValueError):
    """Raised when an automaton description is invalid."""


class DFA:
    """A deterministic finite automaton over an explicit alphabet.

    Parameters
    ----------
    states:
        Iterable of hashable state labels.  Must be non-empty.
    alphabet:
        Iterable of single-character strings.  Every transition symbol
        must belong to this alphabet.
    start:
        The start state.  Must be a member of ``states`` and must not be
        accepting (empty lexemes are not allowed, so a token always
        consumes at least one character).
    transitions:
        Mapping ``(state, char) -> state``.
    accepts:
        Iterable of accepting states, a subset of ``states``.
    """

    def __init__(self, *, states, alphabet, start, transitions, accepts):
        self._states = frozenset(states)
        self._alphabet = frozenset(alphabet)
        self._accepts = frozenset(accepts)
        if not self._states:
            raise DFAError("states must be non-empty")
        for ch in self._alphabet:
            if not isinstance(ch, str) or len(ch) != 1:
                raise DFAError(f"alphabet symbols must be single characters, got {ch!r}")
        if start not in self._states:
            raise DFAError(f"start state {start!r} is not in states")
        unknown_accepts = self._accepts - self._states
        if unknown_accepts:
            raise DFAError(f"accept states not declared: {sorted(unknown_accepts)!r}")
        if start in self._accepts:
            raise DFAError("start state must not be accepting (empty lexemes are not allowed)")
        self._start = start
        self._trans = {}
        for (src, ch), dst in dict(transitions).items():
            if src not in self._states:
                raise DFAError(f"transition from unknown state {src!r}")
            if dst not in self._states:
                raise DFAError(f"transition to unknown state {dst!r}")
            if ch not in self._alphabet:
                raise DFAError(f"transition on symbol {ch!r} outside the alphabet")
            self._trans[(src, ch)] = dst
        self._productive = self._compute_productive()
        self._extendable = frozenset(
            src for (src, _ch), dst in self._trans.items() if dst in self._productive
        )

    def _compute_productive(self):
        # A state is productive when an accept state is reachable from it.
        # Non-productive states can never lead to a token, so the scanner
        # treats them as dead, which tightens the buffering bound.
        reverse = {s: set() for s in self._states}
        for (src, _ch), dst in self._trans.items():
            reverse[dst].add(src)
        seen = set(self._accepts)
        work = list(self._accepts)
        while work:
            state = work.pop()
            for pred in reverse[state]:
                if pred not in seen:
                    seen.add(pred)
                    work.append(pred)
        return frozenset(seen)

    @property
    def states(self):
        return self._states

    @property
    def alphabet(self):
        return self._alphabet

    @property
    def start(self):
        return self._start

    @property
    def accepts(self):
        return self._accepts

    def step(self, state, ch):
        """Return the successor state or ``None`` when no transition exists."""
        return self._trans.get((state, ch))

    def is_accept(self, state):
        return state in self._accepts

    def is_productive(self, state):
        return state in self._productive

    def is_extendable(self, state):
        """True when some transition leads to a state that can still accept.

        A match ending in a non-extendable state can never grow longer,
        so the scanner may decide without waiting for more input.
        """
        return state in self._extendable
