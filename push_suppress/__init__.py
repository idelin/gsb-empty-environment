"""Multi-channel push notification suppression decision component.

Selects a maximum-profit feasible schedule of candidate notifications
subject to per-user rolling-window limits, global per-channel capacity,
mutually exclusive campaign groups, and an optional cost budget.
"""

from .model import Instance, Notification, Send, parse_instance, parse_sends
from .checkers import validate_schedule, is_feasible
from .solver import Solver, SolveResult, solve
from .enumerate import enumerate_schedules, best_by_enumeration

__all__ = [
    "Instance",
    "Notification",
    "Send",
    "parse_instance",
    "parse_sends",
    "validate_schedule",
    "is_feasible",
    "Solver",
    "SolveResult",
    "solve",
    "enumerate_schedules",
    "best_by_enumeration",
]

__version__ = "0.1.0"
