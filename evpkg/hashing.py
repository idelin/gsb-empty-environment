"""Canonical serialization and hashing helpers."""

from __future__ import annotations

import hashlib
import json


def canonical_bytes(obj) -> bytes:
    """Deterministic JSON encoding used for every digest in the system."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def sha256_hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()
