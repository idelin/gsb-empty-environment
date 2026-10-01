"""Bounded random-variable mean estimation with auditable guarantees.

* Staged checking with pre-declared checkpoints and error allocation:
  stop only when the error target is certified; budget exhaustion is
  reported as NOT_MET with full evidence preserved.
* Simultaneous estimation of several variables with adaptive choice of
  the next variable to sample; overall failure probability controlled by
  pre-declared per-variable shares and time-uniform confidence sequences.
  Correlated/shared samples are never treated as independent.
* Exact coverage verification for Bernoulli means on finite domains via
  binomial recursion.
* Randomness is injected by the caller; seeds and sample counts are
  recorded.  Exact conclusions and empirical estimates are labelled.
"""
from .bounds import (
    bernoulli_endpoint_radius,
    cs_radius,
    hoeffding_radius,
    stage_delta,
    validate_delta,
    validate_epsilon,
)
from .evidence import EMPIRICAL, EXACT, MET, NOT_MET, write_evidence
from .finite import (
    CoverageReport,
    bernoulli_coverage_exact,
    binomial_pmf,
    verify_hoeffding_bernoulli,
    verify_radius_bernoulli,
)
from .multi import AdaptiveMultiEstimator, MultiResult, VariableResult, VariableSpec
from .rng import RecordingRandom
from .single import EstimationResult, StageRecord, StagedEstimator

__version__ = "0.1.0"
