"""Exact rational interval arithmetic with affine correlation tracking,
disconnected division domains, singularity-aware domain splitting,
derivation certificates, and rational counterexample sampling.

Pure Python 3.11 standard library (fractions + math); no symbolic
algebra packages.
"""

from .core import (Interval, Domain, DivResult, divide, reciprocal,
                   POS_INF, NEG_INF)
from .affine import (AffineForm, affine_add, affine_sub, affine_mul,
                     affine_neg, affine_scale)
from .certificate import Certificate, Step, certify_range
from .expr import Const, Var, Neg, Add, Sub, Mul, Square, var, const
from .splitting import split_domain, analyze, AnalysisResult
from .sampling import rational_samples, find_counterexample

__all__ = [
    "Interval", "Domain", "DivResult", "divide", "reciprocal",
    "POS_INF", "NEG_INF",
    "AffineForm", "affine_add", "affine_sub", "affine_mul", "affine_neg",
    "affine_scale",
    "Certificate", "Step", "certify_range",
    "Const", "Var", "Neg", "Add", "Sub", "Mul", "Square", "var", "const",
    "split_domain", "analyze", "AnalysisResult",
    "rational_samples", "find_counterexample",
]
