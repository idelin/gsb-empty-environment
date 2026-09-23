"""Error types. Messages must never contain raw field values."""

from __future__ import annotations


class CdcError(Exception):
    """Base error for the CDC pseudonymization engine."""


class PolicyError(CdcError):
    """Raised when an event violates the active policy set.

    Carries only the table/field name, never the raw value.
    """

    def __init__(self, message: str, *, table: str, field: str) -> None:
        super().__init__(message)
        self.table = table
        self.field = field


class MigrationError(CdcError):
    """Raised when an atomic mapping migration fails."""


class ReplayValidationError(CdcError):
    """Raised when synthetic reference-graph replay validation fails."""
