"""Symbolic epsilon-NFA toolkit: interval edges, lazy determinization,
on-demand product-graph inclusion with shortest distinguishing strings."""

from .symbols import Interval, SymbolSet, find_atom, partition, refine_atoms
from .nfa import NFA, Edge, EpsilonEdge, accepts
from .determinize import LazyDFA, SubsetInfo
from .inclusion import (
    Counterexample,
    InclusionChecker,
    InclusionResult,
    InclusionSearch,
    Status,
    StepEvidence,
)


def __getattr__(name):
    # Lazy to keep ``python -m automata.verify`` free of double-import warnings.
    if name == "verify_counterexample":
        from .verify import verify_counterexample

        return verify_counterexample
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")

__all__ = [
    "Interval",
    "SymbolSet",
    "partition",
    "refine_atoms",
    "find_atom",
    "NFA",
    "Edge",
    "EpsilonEdge",
    "accepts",
    "LazyDFA",
    "SubsetInfo",
    "InclusionChecker",
    "InclusionResult",
    "InclusionSearch",
    "Status",
    "Counterexample",
    "StepEvidence",
    "verify_counterexample",
]
