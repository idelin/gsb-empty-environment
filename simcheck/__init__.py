"""Finite-state weak/branching simulation checker (standard library only)."""

from .lts import LTS, TAU
from .fixpoint import Budget, FixpointResult, greatest_simulation
from .strategy import build_strategy, extract_trace, verify_witness

__all__ = [
    "LTS",
    "TAU",
    "Budget",
    "FixpointResult",
    "greatest_simulation",
    "build_strategy",
    "extract_trace",
    "verify_witness",
]
