"""Symbolic finite automata over integer/character interval alphabets.

Pure standard library.  Set operations (intersection, union, difference,
complement, inclusion) work on alphabet partitions, never on enumerated
symbols.
"""
from .intervals import Interval, char_interval, decode, encode, partition
from .nfa import NFA
from .lazy import Complement, Product, SubsetConstruction, run_word
from .search import EmptinessSearch, Status, StepEvidence, Witness
from .inclusion import (
    INCLUDED,
    NOT_INCLUDED,
    UNKNOWN,
    InclusionCache,
    InclusionResult,
    check_inclusion,
    unified_cells,
    verify_counterexample,
)

__all__ = [
    "Interval", "char_interval", "decode", "encode", "partition",
    "NFA",
    "Complement", "Product", "SubsetConstruction", "run_word",
    "EmptinessSearch", "Status", "StepEvidence", "Witness",
    "INCLUDED", "NOT_INCLUDED", "UNKNOWN",
    "InclusionCache", "InclusionResult", "check_inclusion",
    "unified_cells", "verify_counterexample",
]
