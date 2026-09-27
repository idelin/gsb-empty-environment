"""Subset exponential mechanism with quotas, mutex rules, and auditable sampling."""

from .model import (
    Candidate,
    Constraints,
    InfeasibleError,
    enumerate_legal_subsets,
    is_legal,
    utility,
    utility_sensitivity_fixed_size,
)
from .mechanism import (
    DrawRecord,
    ExponentialMechanism,
    SamplingEvidence,
)

__all__ = [
    "Candidate",
    "Constraints",
    "DrawRecord",
    "ExponentialMechanism",
    "InfeasibleError",
    "SamplingEvidence",
    "enumerate_legal_subsets",
    "is_legal",
    "utility",
    "utility_sensitivity_fixed_size",
]

__version__ = "0.1.0"
