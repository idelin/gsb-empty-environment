"""Symmetry-reduced finite-state model checking via process-ID permutation."""

from .model import Model, State, Transition, NO_PID
from .symmetry import (
    canonicalize,
    check_symmetry_condition,
    invert,
    permute_state,
    role_groups,
    role_permutations,
)
from .checker import (
    PENDING,
    SAFE,
    VIOLATED,
    Counterexample,
    Verdict,
    replay,
    verify,
)

__all__ = [
    "Model",
    "State",
    "Transition",
    "NO_PID",
    "canonicalize",
    "check_symmetry_condition",
    "invert",
    "permute_state",
    "role_groups",
    "role_permutations",
    "PENDING",
    "SAFE",
    "VIOLATED",
    "Counterexample",
    "Verdict",
    "replay",
    "verify",
]
