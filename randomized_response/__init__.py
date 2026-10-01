"""Binary and k-ary randomized response: simulation and aggregate estimation.

Synthetic data only.  Statistical experiments in this package are illustrative
and do not constitute a formal privacy proof.
"""
from .channels import (
    RandomizedResponseChannel,
    validate_counts,
    validate_distribution,
    validate_epsilon,
    validate_k,
)
from .estimation import ChannelGroup, EstimationReport, estimate_distribution, estimate_from_counts
from .inference import ConfidenceReport, confidence_intervals
from .simulation import (
    ExactBinaryReference,
    exact_binary_reference,
    simulate_counts,
    theoretical_output_distribution,
)

__all__ = [
    "RandomizedResponseChannel",
    "validate_counts",
    "validate_distribution",
    "validate_epsilon",
    "validate_k",
    "ChannelGroup",
    "EstimationReport",
    "estimate_distribution",
    "estimate_from_counts",
    "ConfidenceReport",
    "confidence_intervals",
    "ExactBinaryReference",
    "exact_binary_reference",
    "simulate_counts",
    "theoretical_output_distribution",
]
