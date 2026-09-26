"""Exact brute-force Pareto enumeration of resource-constrained plan combos."""

from .enumerator import (
    Category,
    CategoryResult,
    EnumerationResult,
    dominates,
    enumerate_combinations,
    keep_equal_value_representatives,
    pareto_front_indices,
)
from .models import Combination, Constraints, Plan, to_fraction

__all__ = [
    "Category",
    "CategoryResult",
    "Combination",
    "Constraints",
    "EnumerationResult",
    "Plan",
    "dominates",
    "enumerate_combinations",
    "keep_equal_value_representatives",
    "pareto_front_indices",
    "to_fraction",
]

__version__ = "0.1.0"

