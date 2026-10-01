"""Domain-isolated pseudonym derivation built on standard HMAC-SHA256.

No custom cryptography: the only primitive is hmac.new(key, msg, hashlib.sha256).
Domain isolation comes from the message encoding, not from ad-hoc construction.
"""
from __future__ import annotations

import hashlib
import hmac

_INFO = b"pseudonym-service/v1"


def _encode_parts(*parts: str) -> bytes:
    """Length-prefixed encoding so (domain, subject, epoch) tuples are unambiguous."""
    out = bytearray(_INFO)
    for part in parts:
        data = part.encode("utf-8")
        out += len(data).to_bytes(8, "big")
        out += data
    return bytes(out)


def derive_digest(key: bytes, domain: str, subject: str, epoch: int = 0) -> str:
    """Deterministic HMAC-SHA256 hex digest for (domain, subject, epoch)."""
    msg = _encode_parts(domain, subject, str(epoch))
    return hmac.new(key, msg, hashlib.sha256).hexdigest()


def format_pseudonym(version: int, digest: str) -> str:
    return f"ps:v{version}:{digest}"


def parse_pseudonym(pseudonym: str) -> tuple[int, str]:
    try:
        prefix, version, digest = pseudonym.split(":", 2)
        if prefix != "ps" or not version.startswith("v"):
            raise ValueError
        return int(version[1:]), digest
    except ValueError:
        raise ValueError(f"malformed pseudonym: {pseudonym!r}") from None
