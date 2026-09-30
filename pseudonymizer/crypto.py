"""Deterministic pseudonym derivation built on standard HMAC-SHA256.

No custom cryptography: the only primitive used is ``hmac`` from the
Python standard library, keyed with caller-provided key material.

Message layout (domain separation is structural, not incidental)::

    INFO || 0x00 || domain || 0x00 || subject || 0x00 || counter(4B BE)

* ``INFO``     - application/version label, separates this system from
                 any other HMAC usage with the same key.
* ``domain``   - isolates pseudonym spaces; the same subject yields
                 different pseudonyms in different domains.
* ``counter``  - deterministic collision-resolution retry index.
"""

from __future__ import annotations

import hashlib
import hmac

INFO = b"pseudonymizer/v1"

MIN_PID_BYTES = 8
MAX_PID_BYTES = 32
DEFAULT_PID_BYTES = 16


def derive_pseudonym(
    key: bytes,
    domain: str,
    subject: str,
    *,
    length: int = DEFAULT_PID_BYTES,
    counter: int = 0,
) -> str:
    """Derive a deterministic hex pseudonym for ``(domain, subject)``.

    Same ``(key, domain, subject, counter)`` always yields the same
    pseudonym; changing any input (including the domain) yields an
    independent one. ``counter`` is used by the deterministic
    collision-resolution flow and is 0 in the common case.
    """
    if not isinstance(key, (bytes, bytearray)) or len(key) == 0:
        raise ValueError("key must be non-empty bytes")
    if not MIN_PID_BYTES <= length <= MAX_PID_BYTES:
        raise ValueError(
            f"length must be within [{MIN_PID_BYTES}, {MAX_PID_BYTES}] bytes"
        )
    if counter < 0:
        raise ValueError("counter must be >= 0")
    message = (
        INFO
        + b"\x00"
        + domain.encode("utf-8")
        + b"\x00"
        + subject.encode("utf-8")
        + b"\x00"
        + counter.to_bytes(4, "big")
    )
    digest = hmac.new(bytes(key), message, hashlib.sha256).digest()
    return digest[:length].hex()
