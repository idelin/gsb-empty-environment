"""Version sets, target conditions and multi-package dependency solving."""

from .index import BuildInput, Dependency, PackageIndex, Release
from .intervals import NEG, POS, IntervalSet
from .brute import brute_all_solutions, brute_solve
from .explain import explain
from .lockdiff import ChangeCause, LockChangeReport, trace_lock_change
from .solver import Requirement, Solution, Solver, Unsatisfiable, UnsatCore, solve
from .spec import SpecError, parse_spec
from .target import Branch, Condition, ConditionalVersionSet, Literal
from .version import Version
from .versionset import RELEASE_DOMAIN, VersionSet

__all__ = [
    "BuildInput",
    "Branch",
    "ChangeCause",
    "Condition",
    "ConditionalVersionSet",
    "Dependency",
    "IntervalSet",
    "LockChangeReport",
    "NEG",
    "POS",
    "PackageIndex",
    "RELEASE_DOMAIN",
    "Requirement",
    "Release",
    "Solution",
    "Solver",
    "SpecError",
    "UnsatCore",
    "Unsatisfiable",
    "Version",
    "VersionSet",
    "brute_all_solutions",
    "brute_solve",
    "explain",
    "parse_spec",
    "solve",
    "trace_lock_change",
]
