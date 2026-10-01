"""Recoverable sparse semiring matrix engine (pure Python 3.11 stdlib)."""

from .engine import Engine, Snapshot, Stats
from .errors import (
    CorruptionError,
    DimensionError,
    DuplicateTxnError,
    EngineError,
    VersionNotRetainedError,
)
from .semirings import (
    INF,
    BooleanSemiring,
    IntSemiring,
    MinPlusSemiring,
    Semiring,
    get_semiring,
)

__all__ = [
    "Engine",
    "Snapshot",
    "Stats",
    "Semiring",
    "BooleanSemiring",
    "IntSemiring",
    "MinPlusSemiring",
    "get_semiring",
    "INF",
    "EngineError",
    "DimensionError",
    "DuplicateTxnError",
    "VersionNotRetainedError",
    "CorruptionError",
]
