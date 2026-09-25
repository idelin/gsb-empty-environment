"""Immutable result objects returned by graph queries."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional, Tuple


@dataclass(frozen=True)
class CycleEvidence:
    """Witness that infinitely many source->target walks exist.

    reach_path: edge ids leading from the source to the cycle vertex.
    cycle:      edge ids forming a closed loop at the cycle vertex.
    exit_path:  edge ids leading from the cycle vertex to the target.
    """

    cycle_vertex: str
    reach_path: Tuple[str, ...]
    cycle: Tuple[str, ...]
    exit_path: Tuple[str, ...]


@dataclass(frozen=True)
class CountResult:
    """Result of a walk-count query over a length interval [lo, hi).

    count is None exactly when infinite is True (unbounded hi with a
    usable cycle). reachable_slice lists the vertices that are both
    reachable from the source and able to reach the target.
    """

    version: int
    source: str
    target: str
    lo: int
    hi: Optional[int]
    modulus: Optional[int]
    count: Optional[int]
    infinite: bool
    evidence: Optional[CycleEvidence]
    reachable_slice: frozenset


@dataclass(frozen=True)
class WalkResult:
    """The k-th walk in lexicographic edge-id order."""

    version: int
    k: int
    edges: Tuple[str, ...]
    vertices: Tuple[str, ...]

    @property
    def length(self) -> int:
        return len(self.edges)
