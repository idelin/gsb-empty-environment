"""Inclusion-minimal fact-support maintenance over a shared rule DAG."""

from .model import ANY, NOT, Fact, Rule
from .repository import Change, GoalView, KnowledgeBase, RevisionReport
from .semantics import (
    VerificationError,
    brute_force_minimal_supports,
    entails,
    min_cardinality_supports,
    verify_evidence,
    wfs_true,
)

__all__ = [
    "ANY",
    "NOT",
    "Fact",
    "Rule",
    "KnowledgeBase",
    "GoalView",
    "Change",
    "RevisionReport",
    "brute_force_minimal_supports",
    "min_cardinality_supports",
    "entails",
    "wfs_true",
    "verify_evidence",
    "VerificationError",
]
