"""Weighted max-min fair bandwidth allocation on shared links.

Pure standard-library implementation (Python 3.11) using exact rational
arithmetic (fractions.Fraction). No external solver is used.

Modules
-------
model        -- demand / link data model (multi-path routes, counted once)
allocator    -- progressive-filling weighted max-min fair allocator
certificate  -- bottleneck-link fairness certificate (build + verify)
reallocator  -- incremental re-allocation after failures / withdrawals
gridverify   -- exhaustive small rational grid verification with budget
"""

from .model import Demand, Network
from .allocator import Allocation, FreezeEvent, allocate
from .certificate import (
    Certificate,
    Witness,
    build_certificate,
    check_feasible,
    verify_certificate,
)
from .reallocator import Reallocator, Reallocation
from .gridverify import (
    Status,
    Verification,
    suggest_step,
    verify_feasible,
    verify_optimal,
)

__all__ = [
    "Demand",
    "Network",
    "Allocation",
    "FreezeEvent",
    "allocate",
    "Certificate",
    "Witness",
    "build_certificate",
    "check_feasible",
    "verify_certificate",
    "Reallocator",
    "Reallocation",
    "Status",
    "Verification",
    "suggest_step",
    "verify_feasible",
    "verify_optimal",
]
