"""licsel - license-expression aware dependency-graph selection solver.

All rules are synthetic modeling inputs from local JSON files; nothing
here is a legal conclusion and no network access is performed.
"""

from .version import Version, VersionConstraint
from .expr import Atom, And, Or, parse, parse_terms, to_terms
from .model import Problem, load_problem, compute_instances
from .solver import Solver, SolveResult, enumerate_optimal
from .core import conflict_core
from .incremental import repair

__all__ = [
    "Version",
    "VersionConstraint",
    "Atom",
    "And",
    "Or",
    "parse",
    "parse_terms",
    "to_terms",
    "Problem",
    "load_problem",
    "compute_instances",
    "Solver",
    "SolveResult",
    "enumerate_optimal",
    "conflict_core",
    "repair",
]
