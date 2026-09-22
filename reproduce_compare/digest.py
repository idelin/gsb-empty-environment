"""Raw byte digests. Nothing in this module normalizes payload bytes."""

from __future__ import annotations

import hashlib


def digest_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()
