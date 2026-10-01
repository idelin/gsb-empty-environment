"""planrepair: repair a feasible plan after task additions / resource failures.

Lexicographic objectives: minimise the number of moved tasks first, then the
total assignment cost.  The executed prefix is frozen.  Pure standard library,
exact rational arithmetic, no external solver.
"""

from .model import Disruption, Plan, Placement, Q, Resource, Task, TimeWindow
from .repair import (
    Change,
    ConflictEvidence,
    RepairResult,
    SearchStats,
    Status,
    repair,
)

__all__ = [
    "Change",
    "ConflictEvidence",
    "Disruption",
    "Plan",
    "Placement",
    "Q",
    "RepairResult",
    "Resource",
    "SearchStats",
    "Status",
    "Task",
    "TimeWindow",
    "repair",
]

__version__ = "0.1.0"
