"""Affine-arithmetic error-propagation DAG with certificates."""

from .core import AffineForm, IntervalContainsZero, NoiseSymbols
from .dag import DAG, EvalResult
from .piecewise import PiecewiseDomainError, PiecewiseLinear, abs_pl, relu_pl
from .certificate import (
    VerifyResult,
    build_certificate,
    certificate_from_json,
    certificate_to_json,
    verify_certificate,
)

__all__ = [
    "AffineForm",
    "DAG",
    "EvalResult",
    "IntervalContainsZero",
    "NoiseSymbols",
    "PiecewiseDomainError",
    "PiecewiseLinear",
    "VerifyResult",
    "abs_pl",
    "build_certificate",
    "certificate_from_json",
    "certificate_to_json",
    "relu_pl",
    "verify_certificate",
]
