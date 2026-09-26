"""Public counting interfaces: exact big integers vs. modular counting.

The two modes are deliberately separate classes. Lexicographic selection
(unranking) and ranking need exact branch sizes, so they exist only on
``ExactCounter``. Asking a ``ModCounter`` for the k-th string raises
``UnsupportedModeError``: residues cannot decide which branch contains k.
"""

from __future__ import annotations

from typing import Iterable, Optional

from .dfa import DFA
from .engine import ProductEngine
from .enumeration import Checkpoint, StringEnumerator
from .errors import UnsupportedModeError
from .witness import InfiniteWitness


class ExactCounter:
    """Intersection queries with unbounded exact integer answers."""

    def __init__(self, dfas: Iterable[DFA]) -> None:
        self._engine = ProductEngine(dfas, mod=None)

    @property
    def alphabet(self):
        return self._engine.alphabet

    @property
    def versions(self):
        return self._engine.versions

    def count_length(self, length: int) -> int:
        return self._engine.count_length(length)

    def count_interval(self, low: int, high: int) -> int:
        return self._engine.count_interval(low, high)

    def kth_string(self, length: int, k: int) -> str:
        return self._engine.kth_string(length, k)

    def kth_in_interval(self, low: int, high: int, k: int) -> str:
        return self._engine.kth_in_interval(low, high, k)

    def rank(self, text: str) -> Optional[int]:
        return self._engine.rank(text)

    def is_infinite(self) -> bool:
        return self._engine.is_infinite()

    def infinite_witness(self) -> Optional[InfiniteWitness]:
        return self._engine.infinite_witness()

    def enumerator(
        self,
        length: int,
        budget: Optional[int] = None,
        checkpoint: Optional[Checkpoint] = None,
    ) -> StringEnumerator:
        return StringEnumerator(self, length, budget=budget, checkpoint=checkpoint)


class ModCounter:
    """Intersection counting reduced modulo a fixed integer."""

    def __init__(self, dfas: Iterable[DFA], mod: int) -> None:
        self._engine = ProductEngine(dfas, mod=mod)
        self.mod = mod

    @property
    def alphabet(self):
        return self._engine.alphabet

    @property
    def versions(self):
        return self._engine.versions

    def count_length(self, length: int) -> int:
        return self._engine.count_length(length)

    def count_interval(self, low: int, high: int) -> int:
        return self._engine.count_interval(low, high)

    def is_infinite(self) -> bool:
        return self._engine.is_infinite()

    def infinite_witness(self) -> Optional[InfiniteWitness]:
        return self._engine.infinite_witness()

    def kth_string(self, *args, **kwargs) -> str:
        raise UnsupportedModeError(
            "modular counts cannot select the k-th string; use ExactCounter"
        )

    def kth_in_interval(self, *args, **kwargs) -> str:
        raise UnsupportedModeError(
            "modular counts cannot select the k-th string; use ExactCounter"
        )

    def rank(self, *args, **kwargs):
        raise UnsupportedModeError(
            "modular counts cannot rank strings; use ExactCounter"
        )

    def enumerator(self, *args, **kwargs) -> StringEnumerator:
        raise UnsupportedModeError(
            "string enumeration requires exact counts; use ExactCounter"
        )
