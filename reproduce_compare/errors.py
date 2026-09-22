"""Shared error types."""

from __future__ import annotations


class RuleError(ValueError):
    """Invalid, overlapping, or target-type-mismatched ignore rule."""


class QuotaExceededError(Exception):
    """Uncompressed-byte or entry-count budget exhausted while reading."""

    def __init__(self, reason: str, consumed: int = 0):
        super().__init__(reason)
        self.reason = reason
        self.consumed = consumed


class CorruptArchiveError(ValueError):
    """ZIP container is structurally damaged or uses an unsupported feature."""
