"""Counterexample/witness for the infinitude of an intersection language."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable

from .dfa import DFA


@dataclass(frozen=True)
class InfiniteWitness:
    """Structured evidence that the language is infinite.

    * ``prefix`` takes every DFA from its start to a state on a cycle;
    * ``cycle`` is a non-empty string looping at that state in every DFA;
    * ``suffix`` takes that state to an accepting state in every DFA.

    Therefore ``prefix + cycle * i + suffix`` is accepted for every ``i >= 0``,
    producing infinitely many distinct strings.
    """

    prefix: str
    cycle: str
    suffix: str

    def __post_init__(self) -> None:
        if not self.cycle:
            raise ValueError("cycle evidence must be a non-empty string")

    def sample(self, repeats: int) -> str:
        if repeats < 0:
            raise ValueError("repeats must be non-negative")
        return self.prefix + self.cycle * repeats + self.suffix

    def verify(self, dfas: Iterable[DFA]) -> bool:
        """Check the witness independently, using raw per-DFA simulation only.

        Confirms the cycle is reachable (via ``prefix``), is a genuine loop
        (via ``cycle``), and can lead to an accepting state (via ``suffix``).
        """
        for dfa in dfas:
            on_cycle = dfa.walk(dfa.start, self.prefix)
            if on_cycle is None:
                return False
            if dfa.walk(on_cycle, self.cycle) != on_cycle:
                return False
            end = dfa.walk(on_cycle, self.suffix)
            if end is None or end not in dfa.accepting:
                return False
        return True
