"""Deterministic, version-keyed pseudonymization via HMAC-SHA256."""

from __future__ import annotations

import hashlib
import hmac
import json


def canonical(value) -> str:
    """Canonical string form so equal values map to equal pseudonyms."""
    return json.dumps(value, sort_keys=True, ensure_ascii=False)


class Pseudonymizer:
    """Derives stable pseudonyms bound to a specific policy version."""

    def __init__(self, version: int, secret: bytes) -> None:
        self.version = version
        self._key = hmac.new(
            secret, f"policy-version:{version}".encode("utf-8"), hashlib.sha256
        ).digest()

    def pseudonym(self, scope: str, value) -> str:
        """Pseudonym for value within a domain scope. Fields sharing a
        scope (PK/FK domain) produce identical pseudonyms."""
        msg = f"{scope}\x00{canonical(value)}".encode("utf-8")
        digest = hmac.new(self._key, msg, hashlib.sha256).hexdigest()
        return f"ps_{digest[:24]}"
