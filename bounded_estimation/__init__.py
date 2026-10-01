"""Bounded random-variable mean estimation with exact staged guarantees."""

from .bernoulli import (
    GridVerification,
    binomial_pmf,
    exact_coverage,
    hoeffding_bernoulli_radius,
    verify_on_grid,
)
from .confidence import (
    constant_observation_radius,
    hoeffding_radius,
    stage_delta,
    stage_sample_sizes,
    trivial_radius,
)
from .estimator import (
    ConclusionKind,
    EstimateResult,
    StageRecord,
    StagedTracker,
    Status,
    estimate_mean,
)
from .multi import (
    AdaptiveMultiEstimator,
    MultiResult,
    SelectionRecord,
    VariableResult,
    VariableSpec,
)

__all__ = [
    "AdaptiveMultiEstimator",
    "ConclusionKind",
    "EstimateResult",
    "GridVerification",
    "MultiResult",
    "SelectionRecord",
    "StageRecord",
    "StagedTracker",
    "Status",
    "VariableResult",
    "VariableSpec",
    "binomial_pmf",
    "constant_observation_radius",
    "estimate_mean",
    "exact_coverage",
    "hoeffding_bernoulli_radius",
    "hoeffding_radius",
    "stage_delta",
    "stage_sample_sizes",
    "trivial_radius",
    "verify_on_grid",
]
