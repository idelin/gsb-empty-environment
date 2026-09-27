"""Exact alias-method sampling with rational weights (stdlib only)."""

from .analysis import empirical_frequencies, enumerate_exact_distribution
from .exact_alias import AliasTable, WeightError, to_exact_weight
from .publisher import AliasTablePublisher, SamplingSession

__all__ = [
    "AliasTable",
    "AliasTablePublisher",
    "SamplingSession",
    "WeightError",
    "empirical_frequencies",
    "enumerate_exact_distribution",
    "to_exact_weight",
]
