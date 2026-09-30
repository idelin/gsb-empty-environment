"""Auditable CSP solver: solver + trail + independent replay/verdict checkers."""
from .model import Constraint, Problem
from .replay import ReplayResult, replay
from .solver import Result, Solver, Status
from .trail import BACKTRACK, CONFLICT, DECIDE, PROPAGATE, Event, Trail
from .verify import check_sat

__all__ = [
    "BACKTRACK", "CONFLICT", "DECIDE", "PROPAGATE",
    "Constraint", "Event", "Problem", "ReplayResult", "Result",
    "Solver", "Status", "Trail", "check_sat", "replay",
]
