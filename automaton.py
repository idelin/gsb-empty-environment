"""Deterministic finite automaton (DFA) definition.

Pure standard library, Python 3.11.
"""

from __future__ import annotations

from typing import Dict, Hashable, Iterable, Mapping, Sequence, Set, Tuple

State = Hashable
Symbol = Hashable


class DFA:
    """A complete deterministic finite automaton.

    ``transitions`` maps ``(state, symbol) -> state`` and must be total over
    ``states x alphabet`` so that every word has a well-defined run.
    """

    def __init__(
        self,
        states: Iterable[State],
        alphabet: Iterable[Symbol],
        transitions: Mapping[Tuple[State, Symbol], State],
        start: State,
        accepting: Iterable[State],
    ) -> None:
        self.states: Set[State] = set(states)
        self.alphabet: Set[Symbol] = set(alphabet)
        self.start = start
        self.accepting: Set[State] = set(accepting)

        if self.start not in self.states:
            raise ValueError("start state is not in the state set")
        if not self.accepting <= self.states:
            raise ValueError("accepting states must be a subset of states")

        self._delta: Dict[Tuple[State, Symbol], State] = {}
        for (state, symbol), target in transitions.items():
            if state not in self.states:
                raise ValueError(f"transition source {state!r} not a state")
            if target not in self.states:
                raise ValueError(f"transition target {target!r} not a state")
            if symbol not in self.alphabet:
                raise ValueError(f"transition symbol {symbol!r} not in alphabet")
            self._delta[(state, symbol)] = target

        for state in self.states:
            for symbol in self.alphabet:
                if (state, symbol) not in self._delta:
                    raise ValueError(
                        f"missing transition for ({state!r}, {symbol!r}); "
                        "the DFA must be complete"
                    )

    def step(self, state: State, symbol: Symbol) -> State:
        return self._delta[(state, symbol)]

    def accepts(self, word: Sequence[Symbol]) -> bool:
        """Independent acceptance check: run the DFA over ``word``.

        The empty word is accepted iff the start state is accepting.
        """
        state = self.start
        for symbol in word:
            state = self.step(state, symbol)
        return state in self.accepting
