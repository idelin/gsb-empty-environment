"""Digest function used for patch preconditions.

Kept behind a module-level function so tests can inject a weak/colliding
digest and prove that content re-checks still reject wrong baselines.
"""
from __future__ import annotations

import hashlib


def digest(content: str) -> str:
    return hashlib.sha256(content.encode("utf-8")).hexdigest()
