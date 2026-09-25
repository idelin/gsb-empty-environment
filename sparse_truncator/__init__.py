"""Sparse truncation of matrix chains with conservative L-inf certificates."""
from .chain import (AllocationResult, Certificate, ChainTruncator,
                    LayerCertificate)
from .intervals import Interval
from .verify import VerificationReport, check_certificate

__all__ = [
    "AllocationResult",
    "Certificate",
    "ChainTruncator",
    "Interval",
    "LayerCertificate",
    "VerificationReport",
    "check_certificate",
]
