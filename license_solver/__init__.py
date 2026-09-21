"""License expression parsing and full dependency-graph selection solving.

All propagation / disclosure / exception rules are synthetic modeling rules
loaded from local JSON. Nothing here is a legal conclusion.
"""

from .version import Version, VersionRange
from .expression import (
    And,
    EffectiveLicense,
    License,
    Or,
    With,
    evaluate,
    or_nodes,
    parse,
)
from .model import (
    Build,
    Repository,
    build_from_dict,
    load_build,
    load_repository,
    repository_from_dict,
)
from .solver import (
    FEASIBLE_UPPER_BOUND,
    INFEASIBLE,
    OPTIMAL,
    UNKNOWN,
    Selection,
    SolveResult,
    Solver,
    evaluate_selection,
)
from .bruteforce import brute_force
from .greedy import greedy_select
from .repair import RepairResult, repair

__all__ = [
    "And",
    "Build",
    "EffectiveLicense",
    "FEASIBLE_UPPER_BOUND",
    "INFEASIBLE",
    "License",
    "OPTIMAL",
    "Or",
    "RepairResult",
    "Repository",
    "Selection",
    "SolveResult",
    "Solver",
    "UNKNOWN",
    "Version",
    "VersionRange",
    "With",
    "brute_force",
    "build_from_dict",
    "evaluate",
    "evaluate_selection",
    "greedy_select",
    "load_build",
    "load_repository",
    "or_nodes",
    "parse",
    "repair",
    "repository_from_dict",
]
