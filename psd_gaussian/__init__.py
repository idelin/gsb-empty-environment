"""PSD covariance decomposition, correlated Gaussian sampling, and auditing.

Standard library only (Python 3.11+).  Randomness is always injected by the
caller; exact conclusions (rational checks, decomposition residuals) are
kept strictly separate from empirical estimates (sample covariances).
"""
from .audit import CovarianceAudit, audit_samples, sample_moments
from .conditional import (
    ConditionalGaussian,
    InfeasibleConditionError,
    condition_gaussian,
)
from .decomposition import (
    Decomposition,
    NotPositiveSemidefiniteError,
    NotSymmetricError,
    PSDTolerance,
    decompose,
)
from .exact import ExactPSDReport, exact_check_psd, exact_verify_decomposition
from .sampling import SamplingRecord, sample_correlated

__all__ = [
    "ConditionalGaussian",
    "CovarianceAudit",
    "Decomposition",
    "ExactPSDReport",
    "InfeasibleConditionError",
    "NotPositiveSemidefiniteError",
    "NotSymmetricError",
    "PSDTolerance",
    "SamplingRecord",
    "audit_samples",
    "condition_gaussian",
    "decompose",
    "exact_check_psd",
    "exact_verify_decomposition",
    "sample_correlated",
    "sample_moments",
]
