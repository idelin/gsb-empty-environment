"""Exact Sturm-sequence real root isolation for rational polynomials.

Pure Python 3.11 standard library (fractions.Fraction); no symbolic
algebra packages. Public API:

* isolate(p, max_steps=None)        -> IsolationResult with certificates
* verify_isolation(result)          -> independent recheck (problem list)
* verify_certificate(p, cert)       -> independent per-root recheck
* revise_polynomial(p, changes)     -> exact coefficient revision
* revise_isolation(old, p_new, ...) -> interval reuse + identity links
"""

from .engine import (NOTES, Interval, IsolationResult, PendingInterval,
                     RootCertificate, isolate)
from .polynomials import (ONE, ZERO, Poly, add, degree, derivative,
                          divmod_poly, evaluate, gcd, is_zero, monic, mul,
                          neg, poly, poly_to_str, scale,
                          squarefree_factorization, sub)
from .revision import (REVISION_NOTES, IdentityLink, RevisionResult,
                       revise_isolation, revise_polynomial)
from .sturm import cauchy_bound, sign_variations, sturm_sequence
from .verify import verify_certificate, verify_isolation

__all__ = [
    "Interval", "IsolationResult", "PendingInterval", "RootCertificate",
    "isolate", "NOTES", "IdentityLink", "RevisionResult",
    "revise_isolation", "revise_polynomial", "REVISION_NOTES",
    "verify_certificate", "verify_isolation",
    "Poly", "poly", "ZERO", "ONE", "add", "sub", "neg", "mul", "scale",
    "divmod_poly", "gcd", "monic", "derivative", "evaluate", "degree",
    "is_zero", "squarefree_factorization", "poly_to_str",
    "sturm_sequence", "sign_variations", "cauchy_bound",
]
