"""Budgeted multi-goal proof search with shared derivations on a finite graph."""

from .engine import BudgetExhausted, ProofEngine, SearchStats
from .exact import ExactResult, exact_max_weight
from .model import (
    Certificate,
    Fact,
    Goal,
    GoalStatus,
    ProofNode,
    Rule,
    replay_certificate,
)
from .scheduler import GreedyScheduler

__all__ = [
    "BudgetExhausted",
    "Certificate",
    "ExactResult",
    "Fact",
    "Goal",
    "GoalStatus",
    "GreedyScheduler",
    "ProofEngine",
    "ProofNode",
    "Rule",
    "SearchStats",
    "exact_max_weight",
    "replay_certificate",
]
