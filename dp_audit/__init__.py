"""Empirical audit of injectable mechanisms on synthetic neighboring datasets.

DISCLAIMER: This package performs *statistical* experiments only. An
empirical audit can detect violations of differential privacy, but it can
NEVER prove that a mechanism satisfies differential privacy. All data
processed here is synthetic.
"""

from .audit import AuditResult, audit_mechanism
from .mechanisms import deterministic_identity, randomized_response

__all__ = [
    "AuditResult",
    "audit_mechanism",
    "deterministic_identity",
    "randomized_response",
]
