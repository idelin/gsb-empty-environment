"""Local JSON-driven dependency resolver (no network access)."""

from .version import Version
from .constraints import Comparator, Constraint
from .registry import PackageRelease, Registry
from .resolver import ResolutionConflict, SourcedConstraint, resolve

__all__ = [
    "Version",
    "Comparator",
    "Constraint",
    "PackageRelease",
    "Registry",
    "ResolutionConflict",
    "SourcedConstraint",
    "resolve",
]
