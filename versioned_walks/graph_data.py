"""Shared structural types kept import-cycle free."""

from __future__ import annotations

from dataclasses import dataclass
from typing import FrozenSet, Tuple


@dataclass(frozen=True)
class Edge:
    eid: str
    src: str
    dst: str


@dataclass(frozen=True)
class Snapshot:
    """Immutable graph state at one version.

    Multi-edges are preserved (edges are distinct by eid) and isolated
    vertices are kept in the vertex set.
    """

    version: int
    vertices: FrozenSet[str]
    edges: Tuple[Edge, ...]


# Structural alias used for type hints in analysis helpers.
SnapshotLike = Snapshot
