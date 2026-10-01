"""invcheck: symbolic (BDD-based) candidate invariant checker.

Checks the base case and one-step preservation of candidate invariants
over finite-state models using shared binary decision diagrams, emits
independently checkable certificates or minimal replayable
counterexamples, and supports incremental candidate correction.
"""

from .bdd import BDDManager, ResourceLimitExceeded
from .checker import (FAIL_BASE, FAIL_STEP, PASS, UNKNOWN, CheckResult,
                      InvariantChecker)
from .certificate import (build_certificate, load_certificate,
                          replay_certificate, save_certificate,
                          verify_certificate)
from .incremental import IncrementalSession
from .model import Model, load_model
from .reference import cross_check, reference_check

__all__ = [
    "BDDManager", "ResourceLimitExceeded",
    "InvariantChecker", "CheckResult", "PASS", "FAIL_BASE", "FAIL_STEP",
    "UNKNOWN",
    "Model", "load_model",
    "build_certificate", "save_certificate", "load_certificate",
    "verify_certificate", "replay_certificate",
    "IncrementalSession",
    "reference_check", "cross_check",
]

__version__ = "0.1.0"
