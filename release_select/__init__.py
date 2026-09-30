"""Minimal-diff release selection.

Selects a maximum-benefit set of changes subject to:
  - dependency constraints (dependency cycles are condensed into
    strongly connected components that must be taken as a whole),
  - mutual-exclusion groups,
  - an integer resource capacity.

Ties are broken by (1) fewest selected changes, then (2) lexicographic
order of the sorted change-ID list.
"""

from .core import (
    Change,
    Instance,
    Solution,
    VerificationReport,
    exhaustive_verify,
    parse_instance,
    solve,
)

__all__ = [
    "Change",
    "Instance",
    "Solution",
    "VerificationReport",
    "exhaustive_verify",
    "parse_instance",
    "solve",
]

__version__ = "1.0.0"
