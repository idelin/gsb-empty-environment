"""Stable computation of Poisson-subsampling amplification for pure DP.

Implements eps' = log(1 + q*(exp(eps) - 1)) with:
  * numerically stable float evaluation (no exp overflow / clipping),
  * a parameter expression DAG with outward-rounded interval evaluation,
  * inversion: largest q for a target budget, with a rigorous bracket
    and residual evidence,
  * an independent high-precision series reference for cross-checking,
  * strict applicability validation (inputs outside the sampling model
    are rejected, not silently plugged into the formula).

Scope: synthetic parameters only.  Numerical accuracy of these routines
is evidence about arithmetic, NOT a privacy proof; statistical experiments
are not formal privacy proofs either.
"""

from .core import log_expm1, subsampled_epsilon
from .dag import (
    Add,
    Const,
    ExpM1,
    Log1P,
    Mul,
    Param,
    SubsampleAmp,
    amp_interval,
    compose_rounds,
)
from .intervals import Interval
from .invert import MaxQResult, max_q_for_budget
from .reference import reference_amp, series_exp, series_ln
from .validation import (
    ApplicabilityError,
    SamplingModel,
    validate_subsample_applicability,
)

SYNTHETIC_DATA_ONLY = (
    "This package computes with synthetic parameters only; it never "
    "touches real datasets."
)
NOT_A_PRIVACY_PROOF = (
    "Numerically accurate evaluation of the amplification formula is "
    "arithmetic evidence, not a privacy proof. Statistical experiments "
    "are not formal privacy proofs."
)

__all__ = [
    "Add",
    "ApplicabilityError",
    "Const",
    "ExpM1",
    "Interval",
    "Log1P",
    "MaxQResult",
    "Mul",
    "NOT_A_PRIVACY_PROOF",
    "Param",
    "SYNTHETIC_DATA_ONLY",
    "SamplingModel",
    "SubsampleAmp",
    "amp_interval",
    "compose_rounds",
    "log_expm1",
    "max_q_for_budget",
    "reference_amp",
    "series_exp",
    "series_ln",
    "subsampled_epsilon",
    "validate_subsample_applicability",
]
