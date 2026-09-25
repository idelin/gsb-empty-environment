"""Evidence coverage engine.

Computes satisfied intervals and gaps for conclusion rules that require
several evidence categories, each with "at least k independent sources",
with cross-category source exclusivity.  All facts, rules and steps carry
stable IDs and versions; evidence is replayable.
"""

from .model import Evidence, CategoryRequirement, Rule
from .engine import Evaluation, evaluate, satisfied_at, sources_by_category
from .subsets import SubsetResult, minimal_subset
from .replay import replay, digest

__all__ = [
    "Evidence",
    "CategoryRequirement",
    "Rule",
    "Evaluation",
    "evaluate",
    "satisfied_at",
    "sources_by_category",
    "SubsetResult",
    "minimal_subset",
    "replay",
    "digest",
]
