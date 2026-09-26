"""Exact rational discrete Laplace count mechanism + numeric continuous Laplace.

All data handled by this package is synthetic; callers construct integer
counts directly. See README.md for definitions and guarantees.
"""

from .discrete import (
    DiscreteLaplaceCount,
    NoisyCountRecord,
    ParameterError,
    VerificationReport,
    rational_decay_for_epsilon,
    verify_dp,
)
from .continuous import LaplaceRecord, add_laplace_noise, laplace_inverse_cdf

__all__ = [
    "DiscreteLaplaceCount",
    "NoisyCountRecord",
    "ParameterError",
    "VerificationReport",
    "rational_decay_for_epsilon",
    "verify_dp",
    "LaplaceRecord",
    "add_laplace_noise",
    "laplace_inverse_cdf",
]
