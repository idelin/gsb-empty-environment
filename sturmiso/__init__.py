"""Exact Sturm-sequence isolation of distinct real roots for univariate
polynomials over Q, with certificates, budget interruption and conservative
root-identity matching across parameter revisions.

Standard library only; no symbolic algebra packages.
"""
from .certificates import RootCountCertificate
from .intervals import Interval
from .isolate import (
    IsolationResult,
    PendingInterval,
    RootIsolation,
    cauchy_bound,
    count_with_cert,
    isolate,
    resume_isolation,
)
from .poly import normalize, to_string
from .revision import (
    RevisionReport,
    RootGroup,
    ReusedInterval,
    revise_polynomial,
)
from .squarefree import SquareFreeFactor, squarefree_factorization
from .sturm import EndpointSign, count_distinct_roots, endpoint_sign, sturm_chain
from .verify import (
    verify_count_certificate,
    verify_isolation,
    verify_revision,
    verify_root,
    verify_squarefree_factorization,
)

__all__ = [
    "EndpointSign",
    "Interval",
    "IsolationResult",
    "PendingInterval",
    "RevisionReport",
    "RootCountCertificate",
    "RootGroup",
    "RootIsolation",
    "ReusedInterval",
    "SquareFreeFactor",
    "cauchy_bound",
    "count_distinct_roots",
    "count_with_cert",
    "endpoint_sign",
    "isolate",
    "normalize",
    "resume_isolation",
    "revise_polynomial",
    "squarefree_factorization",
    "sturm_chain",
    "to_string",
    "verify_count_certificate",
    "verify_isolation",
    "verify_revision",
    "verify_root",
    "verify_squarefree_factorization",
]
