"""Finite-state model checker with independently verifiable certificates."""

from .model import FiniteModel, ModelValidationError
from .checker import (
    CheckOutcome,
    CheckResult,
    check_safety,
    explore_reachable,
    reachable_sets,
)
from .verifier import (
    Finding,
    VerificationVerdict,
    verify_certificate,
    verify_counterexample,
    verify_reachability,
)

__all__ = [
    "FiniteModel",
    "ModelValidationError",
    "CheckOutcome",
    "CheckResult",
    "check_safety",
    "explore_reachable",
    "reachable_sets",
    "Finding",
    "VerificationVerdict",
    "verify_certificate",
    "verify_counterexample",
    "verify_reachability",
]
