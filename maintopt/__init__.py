"""Synthetic maintenance-window selection toolkit.

Risk scores are used strictly as in-problem weights; they are NOT
interpreted as real-world failure probabilities.
"""

from .model import Device, Instance
from .planner import BUDGET_UNDECIDED, FEASIBLE, OPTIMAL, SearchResult, solve
from .verify import check_feasible, plan_cost, plan_score, segments_of

__all__ = [
    "Device",
    "Instance",
    "SearchResult",
    "solve",
    "check_feasible",
    "plan_cost",
    "plan_score",
    "segments_of",
    "OPTIMAL",
    "FEASIBLE",
    "BUDGET_UNDECIDED",
]
