"""bootstrap_ci: bootstrap confidence intervals with explicit resampling units.

Design guarantees
-----------------
* Resampling is by observational unit (observation / pair / stratum / block);
  within-unit correlation is never broken by row-wise independent sampling.
* Paired samples are always resampled as whole pairs.
* Quantile definitions are explicit: Hyndman-Fan type 7 for Monte Carlo
  distributions, inverse-CDF (type 1) for exact enumerated distributions.
* Degenerate bootstrap distributions are reported separately
  (``result.degenerate``); identical-value samples yield a point interval.
* Percentile and BCa are separate implementations; a degenerate jackknife or
  non-estimable acceleration raises DegenerateJackknifeError /
  BcaNotEstimableError instead of returning a misleading interval.
* Invalid replicates are never silently dropped (policy "raise" by default;
  "omit" reports invalid_count explicitly).
* Randomness is injected by the caller as a RandomSource; streams are keyed
  by sample ID so parallel evaluation order cannot change results.  The seed
  and sample size are recorded in every result.
* Exact (enumerated) and Monte Carlo (empirical) results are labelled
  separately via ``result.kind``.
"""
from .core import BootstrapResult, bootstrap
from .errors import (
    BcaNotEstimableError,
    BootstrapError,
    DegenerateJackknifeError,
    EnumerationTooLargeError,
    InvalidResampleError,
    UnsupportedMethodError,
)
from .exact import ExactResult, bootstrap_exact
from .intervals import bca_interval, jackknife_acceleration, percentile_interval
from .plans import IIDPlan, MovingBlockPlan, PairedPlan, StratifiedPlan
from .quantile import quantile_type7, weighted_quantile_inverse_cdf
from .rng import RandomSource

__all__ = [
    "BcaNotEstimableError",
    "BootstrapError",
    "BootstrapResult",
    "DegenerateJackknifeError",
    "EnumerationTooLargeError",
    "ExactResult",
    "IIDPlan",
    "InvalidResampleError",
    "MovingBlockPlan",
    "PairedPlan",
    "RandomSource",
    "StratifiedPlan",
    "UnsupportedMethodError",
    "bca_interval",
    "bootstrap",
    "bootstrap_exact",
    "jackknife_acceleration",
    "percentile_interval",
    "quantile_type7",
    "weighted_quantile_inverse_cdf",
]

__version__ = "0.1.0"
