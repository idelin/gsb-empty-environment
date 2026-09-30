"""Domain-separated, HMAC-based pseudonymization with key rotation.

Synthetic data only. See README.md for parameters, definitions and the
scope of the security claims (statistical observations are illustrative
and are not a formal privacy proof).
"""

from .audit import AuditLog
from .crypto import derive_pseudonym
from .errors import (
    ConsistencyError,
    DuplicateKeyVersion,
    OperationCancelled,
    PseudonymError,
    UnknownPseudonym,
)
from .keystore import KeyStore, KeyVersion
from .store import PseudonymStore

__all__ = [
    "AuditLog",
    "ConsistencyError",
    "DuplicateKeyVersion",
    "KeyStore",
    "KeyVersion",
    "OperationCancelled",
    "PseudonymError",
    "PseudonymStore",
    "UnknownPseudonym",
    "derive_pseudonym",
]
